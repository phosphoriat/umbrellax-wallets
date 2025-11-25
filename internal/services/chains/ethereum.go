package chains

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"

	"wallets/internal/config"
	"wallets/internal/database/vault"
	"wallets/internal/services/api/aml"
	"wallets/internal/services/api/bybit"
	"wallets/internal/utils"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Ethereum RPC connection struct
type Ethereum struct {
	BaseChainService
	Client *ethclient.Client
}

// Global package vars
var (
	ethInstance *Ethereum
	ethOnce     sync.Once
)

// GetEthereum Get Ethereum chain instance
func GetEthereum() *Ethereum {
	ethOnce.Do(func() {
		ethInstance = newEthereum()
	})
	return ethInstance
}

// @private newEthereum Create new instance of Ethereum (private constructor)
func newEthereum() *Ethereum {
	// Load config
	cfg, _ := config.Load()

	// Create new RPC client
	ethClient, err := ethclient.Dial(cfg.EthRpcUrl)
	if err != nil {
		log.Printf("failed to connect Ethereum RPC API: %v", err)
	}

	networkCode := "ethereum"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	return &Ethereum{
		BaseChainService: BaseChainService{
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
		},
		Client: ethClient,
	}
}

// GetCode Get network code
// Returns: Network code
func (eth *Ethereum) GetCode() string {
	return eth.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
// userId: Unique user id
// Returns: User HD-wallet address
func (eth *Ethereum) GetUserWallet(userId int) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + eth.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	// Derive user address from master private key
	derivedAddr, err := eth.deriveAddress(masterData["privateKey"].(string), uint32(userId))
	if err != nil {
		return "", err
	}

	return derivedAddr, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of mster wallet info (address and balances of all available tokens)
func (eth *Ethereum) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + eth.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Get ERC20 tokens balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[eth.NetworkCode].(map[string]string) {
		tokenBalance, _ := eth.getErc20Balance(contractAddr, masterAddr)
		balances[token] = tokenBalance
	}

	// Get ETH native balance
	ethBalance, _ := eth.getNativeBalance(masterAddr)
	balances[eth.NativeToken] = ethBalance

	return map[string]any{
		"address":  masterAddr,
		"balances": balances,
	}, nil
}

// GetTransactionCommission Get transaction commission
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// Returns: Map of all fee types with their values (chain_fee, bybit_fee, aml_fee)
func (eth *Ethereum) GetTransactionCommission(currency, toAddr string, amount float64, memo string) (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + eth.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Calc chain fee for currency
	var chainFee float64 = 0
	switch currency {
	case "ETH":
		chainFee, _ = eth.calcNativeFee(masterAddr, toAddr, amount)

	default:
		chainFee, _ = eth.calcErc20Fee(currency, masterAddr, toAddr, amount)
	}

	// Get bybit withdrawal fee
	bybitFee, _ := bybit.GetHTTPClient().GetWithdrawalFee(currency, eth.NetworkCode)

	// Get AML check cost from config
	cfg, _ := config.Load()
	amlFee := cfg.AmlCheckCost

	return map[string]any{
		"chain_fee": chainFee,
		"bybit_fee": bybitFee,
		"aml_fee":   amlFee,
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status
func (eth *Ethereum) GetTransactionStatus(hash, toAddr string) (string, error) {
	_, isPending, err := eth.Client.TransactionByHash(context.Background(), common.HexToHash(hash))
	if err != nil {
		return "", fmt.Errorf("failed to get transaction %s: %w", hash, err)
	}
	if isPending {
		return "pending", nil
	}

	receipt, err := eth.Client.TransactionReceipt(context.Background(), common.HexToHash(hash))
	if err != nil {
		return "", fmt.Errorf("failed to get transaction receipt %s: %w", hash, err)
	}

	if receipt.Status == 1 {
		return "success", nil
	}
	return "failed", nil
}

// SendTransaction Send transaction to the network
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// Returns: Transaction hash
func (eth *Ethereum) SendTransaction(currency, toAddr string, amount float64, memo string) (string, string, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + eth.NetworkCode)
	if err != nil {
		return "", "", fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return "", "", fmt.Errorf("invalid or missing %s master address in Vault", eth.NetworkCode)
	}

	// AML check to address
	amlId, riskLevel, err := aml.GetClient().CheckAddress(eth.NetworkCode, toAddr)
	if err != nil {
		return "", "", fmt.Errorf("AML check address '%s' at network '%s' failed: %w", toAddr, eth.NetworkCode, err)
	}
	// If high or severe AML risks detected, return error without sending transaction
	if riskLevel == "high" || riskLevel == "severe" {
		return "", amlId, fmt.Errorf("failed to send withdrawal transaction to '%s' in '%s': %s AML risk", toAddr, eth.NetworkCode, riskLevel)
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "ETH":
		hash, err = eth.sendNative(masterAddr, toAddr, amount)
	default:
		hash, err = eth.sendErc20(currency, masterAddr, toAddr, amount)
	}

	if err != nil {
		return "", "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, amlId, nil
}

// GetInboundTransactions Get new inbound transactions list
func (eth *Ethereum) GetInboundTransactions() ([]map[string]any, error) {

}

/* GetUserWallet private methods group */
// @private deriveAddress Derive HD-wallet address by derivation index from master wallet private key
// masterPrivateKey: Master wallet private key
// userId: Unique user id for derivation index
// Returns: Derived address
func (eth *Ethereum) deriveAddress(masterPrivateKey string, userId uint32) (string, error) {
	// Get bytes from private key
	bytes, _ := hex.DecodeString(masterPrivateKey)
	// Create new master node from bytes for use in creating HD keychain
	masterHdKey, err := hdkeychain.NewMaster(bytes, &chaincfg.MainNetParams)
	if err != nil {
		return "", fmt.Errorf("failed to derive master key: %w", err)
	}

	// BIP44 derivation: m/44'/60'/0'/0/{index} (accountKey = 0)
	purposeKey, _ := masterHdKey.Derive(hdkeychain.HardenedKeyStart + 44)
	coinKey, _ := purposeKey.Derive(hdkeychain.HardenedKeyStart + 60)
	accountKey, _ := coinKey.Derive(hdkeychain.HardenedKeyStart + 0)
	changeKey, _ := accountKey.Derive(0)
	finalKey, err := changeKey.Derive(userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive final key: %w", err)
	}

	// Get btcec private key
	btcecPrivateKey, _ := finalKey.ECPrivKey()
	// Convert to ecdsa private key
	privateKeyEcdsa := btcecPrivateKey.ToECDSA()

	// Get ecdsa public key from ecdsa private key
	publicKey := privateKeyEcdsa.Public()
	publicKeyEcdsa, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("failed to convert to ECDSA public key")
	}

	// Get address from ecdsa public key
	address := crypto.PubkeyToAddress(*publicKeyEcdsa)
	return address.Hex(), nil
}

/* GetMasterWallet private methods group */
// @private getNativeBalance Get wallet ETH balance
// addr: Wallet address
// Returns: ETH wallet balance
func (eth *Ethereum) getNativeBalance(addr string) (float64, error) {
	// Get ethereum address instance
	ethAddr := common.HexToAddress(addr)

	// Get raw balance (in WEI)
	rawBalance, err := eth.Client.BalanceAt(context.Background(), ethAddr, nil)
	if err != nil {
		return 0, err
	}

	// Get ETH decimals from config
	ethDecimals, _ := config.GetChainSetting[int64](config.TokenSettings, fmt.Sprintf("%s.ETH.decimals", eth.NetworkCode))
	// Return converted balance (in ETH)
	return utils.RemoveDecimals(rawBalance, ethDecimals), nil
}

// @private getErc20Balance Get wallet ERC20 token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: ERC20 token wallet balance
func (eth *Ethereum) getErc20Balance(contractAddr, addr string) (float64, error) {
	// Get token ABI from config
	erc20Abi, err := config.GetChainSetting[string](config.NetworkSettings, fmt.Sprintf("%s.abi", eth.NetworkCode))
	parsedAbi, err := abi.JSON(strings.NewReader(erc20Abi))
	if err != nil {
		return 0, fmt.Errorf("failed to parse ABI: %w", err)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), parsedAbi, eth.Client, eth.Client, eth.Client)

	// Get raw balance (in decimal)
	err = contract.Call(&bind.CallOpts{}, &out, "balanceOf", common.HexToAddress(addr))
	if err != nil {
		return 0, fmt.Errorf("failed to get contract balance: %w", err)
	}
	rawBalance := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	// Get contract decimals
	err = contract.Call(&bind.CallOpts{}, &out, "decimals")
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}
	decimalsValue := *abi.ConvertType(out[0], new(int64)).(*int64)

	// Return converted balance with removed decimals
	return utils.RemoveDecimals(rawBalance, decimalsValue), nil
}

/* GetTransactionCommission private methods group */
// @private calcNativeFee Calc fee for ETH transaction
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in ETH
func (eth *Ethereum) calcNativeFee(fromAddr, toAddr string, amount float64) (float64, error) {
	// Get WEI decimals from config
	weiDecimals, _ := config.GetChainSetting[int64](config.TokenSettings, fmt.Sprintf("%s.ETH.decimals", eth.NetworkCode))
	// Convert amount to WEI
	amountWei := utils.ApplyDecimals(amount, weiDecimals)

	// Get converted from and to addresses
	from := common.HexToAddress(fromAddr)
	to := common.HexToAddress(toAddr)

	// Get estimated gas limit
	gasLimit, err := eth.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  from,
		To:    &to,
		Value: amountWei,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get estimate gas limit: %w", err)
	}

	// Get current gas price
	gasPrice, err := eth.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get current gas price: %w", err)
	}

	// Get total fee in WEI
	totalFeeWei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	// Convert total fee to ETH
	return utils.RemoveDecimals(totalFeeWei, weiDecimals), nil
}

// @private calcErc20Fee Calc fee for ERC20 token transaction
// currency: Token ticker
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in ETH
func (eth *Ethereum) calcErc20Fee(currency, fromAddr, toAddr string, amount float64) (float64, error) {
	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](config.ContractsAddresses, fmt.Sprintf("%s.%s", eth.NetworkCode, currency))
	if err != nil {
		return 0, fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get token ABI from config
	erc20Abi, err := config.GetChainSetting[string](config.NetworkSettings, fmt.Sprintf("%s.abi", eth.NetworkCode))
	parsedAbi, err := abi.JSON(strings.NewReader(erc20Abi))
	if err != nil {
		return 0, fmt.Errorf("failed to parse ABI: %w", err)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), parsedAbi, eth.Client, eth.Client, eth.Client)

	// Get contract decimals
	err = contract.Call(&bind.CallOpts{}, &out, "decimals")
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}
	decimalsValue := *abi.ConvertType(out[0], new(int64)).(*int64)
	// Convert amount to WEI with contract decimals
	amountWei := utils.ApplyDecimals(amount, decimalsValue)

	// Get of converted to address and contract address
	to := common.HexToAddress(toAddr)
	contractAddress := common.HexToAddress(contractAddr)
	// Set params for transfer contract call
	data, err := parsedAbi.Pack("transfer", to, amountWei)

	// Get estimated gas limit
	gasLimit, err := eth.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  common.HexToAddress(fromAddr),
		To:    &contractAddress,
		Value: big.NewInt(0),
		Data:  data,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get estimate gas limit: %w", err)
	}
	// Apply 10% buffer gas limit
	gasLimit = gasLimit + (gasLimit / 10)

	// Get current gas price
	gasPrice, err := eth.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Get total fee in WEI
	totalFeeWei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	// Get WEI decimals from config
	weiDecimals, _ := config.GetChainSetting[int64](config.TokenSettings, fmt.Sprintf("%s.ETH.decimals", eth.NetworkCode))
	// Convert total fee to ETH
	return utils.RemoveDecimals(totalFeeWei, weiDecimals), nil
}

/* SendTransaction private methods group */
// @private sendNative Send native ETH transaction
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Transaction hash
func (eth *Ethereum) sendNative(fromAddr, toAddr string, amount float64) (string, error) {
	// Get decimals from config
	weiDecimals, _ := config.GetChainSetting[int64](config.TokenSettings, fmt.Sprintf("%s.ETH.decimals", eth.NetworkCode))
	// Convert amount to WEI
	amountWei := utils.ApplyDecimals(amount, weiDecimals)

	// Get converted from and to address
	from := common.HexToAddress(fromAddr)
	to := common.HexToAddress(toAddr)

	// Get estimated gas limit
	gasLimit, err := eth.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  from,
		To:    &to,
		Value: amountWei,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get estimate gas limit: %w", err)
	}

	// Get current gas price
	gasPrice, err := eth.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get current gas price: %w", err)
	}

	// Get nonce for sender
	nonce, err := eth.Client.PendingNonceAt(context.Background(), common.HexToAddress(fromAddr))
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %w", err)
	}

	// Create new transaction
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    amountWei,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     nil,
	})

	// Sign, broadcast transaction to the network and return hash
	return eth.signAndBroadcast(tx)
}

// @private sendErc20 Send ERC20 token transaction
// currency: Token ticker
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Transaction hash
func (eth *Ethereum) sendErc20(currency, fromAddr, toAddr string, amount float64) (string, error) {
	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](config.ContractsAddresses, fmt.Sprintf("%s.%s", eth.NetworkCode, currency))
	if err != nil {
		return "", fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get token ABI from config
	erc20Abi, err := config.GetChainSetting[string](config.NetworkSettings, fmt.Sprintf("%s.abi", eth.NetworkCode))
	parsedAbi, err := abi.JSON(strings.NewReader(erc20Abi))
	if err != nil {
		return "", fmt.Errorf("failed to parse ABI: %w", err)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), parsedAbi, eth.Client, eth.Client, eth.Client)

	// Get contract decimals
	err = contract.Call(&bind.CallOpts{}, &out, "decimals")
	if err != nil {
		return "", fmt.Errorf("failed to get contract decimals: %w", err)
	}
	decimalsValue := *abi.ConvertType(out[0], new(int64)).(*int64)
	// Convert amount to WEI with contract decimals
	amountWei := utils.ApplyDecimals(amount, decimalsValue)

	// Get of converted to address and contract address
	to := common.HexToAddress(toAddr)
	contractAddress := common.HexToAddress(contractAddr)
	// Set params for transfer contract call
	data, err := parsedAbi.Pack("transfer", to, amountWei)

	// Get estimated gas limit
	gasLimit, err := eth.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  common.HexToAddress(fromAddr),
		To:    &contractAddress,
		Value: big.NewInt(0),
		Data:  data,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get estimate gas limit: %w", err)
	}
	// Apply 10% buffer gas limit
	gasLimit = gasLimit + (gasLimit / 10)

	// Get current gas price
	gasPrice, err := eth.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get current gas price: %w", err)
	}

	// Get nonce for sender
	nonce, err := eth.Client.PendingNonceAt(context.Background(), common.HexToAddress(fromAddr))
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %w", err)
	}

	// Create new transaction
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &contractAddress,
		Value:    big.NewInt(0),
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     data,
	})

	// Sign, broadcast transaction to the network and return hash
	return eth.signAndBroadcast(tx)
}

// @private signAndBroadcast Sign and broadcast transaction to the network
// tx: Transaction struct
// Returns: Transaction hash
func (eth *Ethereum) signAndBroadcast(tx *types.Transaction) (string, error) {
	// Get master wallet private key from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + eth.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	// Convert hex private key string to *ecdsa.PrivateKey
	privateKey, err := crypto.HexToECDSA(masterData["privateKey"].(string))
	if err != nil {
		return "", fmt.Errorf("invalid or missing %s master private key in Vault", eth.NetworkCode)
	}

	// Get chain ID
	chainID, err := eth.Client.NetworkID(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get chain ID: %w", err)
	}

	// Sign transaction (EIP-155)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign tx: %w", err)
	}

	// Send transaction
	if err := eth.Client.SendTransaction(context.Background(), signedTx); err != nil {
		return "", fmt.Errorf("failed to send tx: %w", err)
	}

	// Return transaction hash
	return signedTx.Hash().Hex(), nil
}

/* ProcessInboundTransactions private methods group */
