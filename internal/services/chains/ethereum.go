package chains

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/database/vault"
	"wallets/internal/repository"
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
	"github.com/ethereum/go-ethereum/rpc"
	"golang.org/x/time/rate"
)

// Ethereum RPC connection struct
type Ethereum struct {
	BaseChainService
	Client          *ethclient.Client
	RateLimit       int
	decimalsCache   map[string]int64
	decimalsCacheMu sync.RWMutex
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
	ethClient, err := ethclient.Dial(cfg.EthRpcUrl + "/" + cfg.QuickNodeApiKey)
	if err != nil {
		log.Printf("[Ethereum] Failed to connect to RPC API: %v", err)
	}

	networkCode := "ethereum"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	return &Ethereum{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
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
// Returns: Map of master wallet info (address and balances of all available tokens)
func (eth *Ethereum) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + eth.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Get ERC20 token balances
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
// oType: Operation type (withdrawal/inner)
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// Returns: Map of all fee types (chain_fee, bybit_fee, aml_fee)
func (eth *Ethereum) GetTransactionCommission(
	oType, wType string, wId *string, currency, toAddr string, amount float64, memo string,
) (map[string]float64, error) {
	var walletPath string
	if wType == "master" {
		walletPath = "/master/" + eth.NetworkCode
	} else {
		if wId == nil {
			return nil, fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, eth.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet address
	fromAddr, ok := walletData["address"].(string)
	if !ok || fromAddr == "" {
		return nil, fmt.Errorf("invalid or missing %s wallet address in Vault", eth.NetworkCode)
	}

	// Calc chain fee for currency (in ETH value)
	var chainFee float64 = 0
	switch currency {
	case "ETH":
		chainFee, err = eth.calcNativeFee(fromAddr, toAddr, amount)
	default:
		chainFee, err = eth.calcErc20Fee(currency, fromAddr, toAddr, amount)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction commission: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetHTTPClient().GetWithdrawalFee(currency, eth.NetworkCode)

		// Get AML check cost from config (in USD value)
		cfg, _ := config.Load()
		amlFee, _ = strconv.ParseFloat(cfg.AmlCheckCost, 64)
	}

	return map[string]float64{
		"chain_fee": chainFee, // In ETH
		"bybit_fee": bybitFee, // In currency value
		"aml_fee":   amlFee,   // in USD
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status (success/pending/failed)
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
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// Returns: Transaction hash
func (eth *Ethereum) SendTransaction(wType string, wId *string, currency, toAddr string, amount float64, memo string) (string, error) {
	var walletPath string
	if wType == "master" {
		walletPath = fmt.Sprintf("/master/%s", eth.NetworkCode)
	} else {
		if wId == nil {
			return "", fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, eth.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return "", fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet address and private key
	fromAddr, okF := walletData["address"].(string)
	privateKey, okP := walletData["privateKey"].(string)
	if !okF || !okP || fromAddr == "" {
		return "", fmt.Errorf("invalid or missing %s wallet data in Vault", eth.NetworkCode)
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "ETH":
		hash, err = eth.sendNative(fromAddr, toAddr, amount, privateKey)
	default:
		hash, err = eth.sendErc20(currency, fromAddr, toAddr, amount, privateKey)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, nil
}

// GetInboundTransactions Get new inbound transactions list
func (eth *Ethereum) GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error) {
	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(eth.RateLimit), 1)

	// Get current block number
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	currentBlockNum, err := eth.Client.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get current block number: %w", eth.NetworkCode, err)
	}

	// Get last block number from Redis (or use current block number, if not exists)
	lastBlock, err := redis.GetClient().HGet(ctx, "last_blocks", eth.NetworkCode)
	var lastBlockNumber uint64
	if err != nil || lastBlock == "" {
		lastBlockNumber = currentBlockNum - 1
		log.Printf("[%s] First run, starting from block %d (current: %d)",
			eth.NetworkCode, lastBlockNumber, currentBlockNum)
	} else {
		parsed, _ := strconv.ParseUint(lastBlock, 10, 64)
		lastBlockNumber = parsed
	}

	// Check if current block not equal or less, than last block
	if currentBlockNum <= lastBlockNumber {
		log.Printf("[%s] No new blocks to process", eth.NetworkCode)
		return []*InboundTransaction{}, nil
	}

	// Get all address for network from DB
	addressList, err := eth.WalletRepo.GetAddressListByNetwork(eth.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get wallet address list: %w", eth.NetworkCode, err)
	}

	// Get current chain id
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	chainID, err := eth.Client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get chain ID: %w", eth.NetworkCode, err)
	}

	// Get ETH decimals from config
	ethDecimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", eth.NetworkCode, "ETH"),
	)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get ETH decimals from config: %w", eth.NetworkCode, err)
	}

	// Get token ABI from config
	erc20Abi, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.abi", eth.NetworkCode),
	)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get ERC20 ABI from config: %w", eth.NetworkCode, err)
	}
	parsedAbi, err := abi.JSON(strings.NewReader(erc20Abi))
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to parse ERC20 ABI from config: %w", eth.NetworkCode, err)
	}

	log.Printf("[%s] Processing %d blocks from %d to %d",
		eth.NetworkCode, currentBlockNum-lastBlockNumber, lastBlockNumber+1, currentBlockNum)

	// Transaction list for all blocks
	txList := make([]*InboundTransaction, 0)

	// Check all new blocks
	for blockNum := lastBlockNumber + 1; blockNum <= currentBlockNum; blockNum++ {
		// Get block entity by number
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
		block, err := eth.Client.BlockByNumber(ctx, big.NewInt(int64(blockNum)))
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to get block %d: %w", eth.NetworkCode, blockNum, err)
		}

		// Get all block receipts (for check status)
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
		blockReceipts, err := eth.Client.BlockReceipts(ctx, rpc.BlockNumberOrHashWithHash(block.Hash(), true))
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to get block receipts: %w", eth.NetworkCode, err)
		}

		log.Printf("[%s] Processing block %d with %d transactions",
			eth.NetworkCode, blockNum, len(block.Transactions()))

		// Process transactions in block
		for index, tx := range block.Transactions() {
			// Get tx hash
			txHash := tx.Hash().Hex()
			// Skip, if transaction is not success
			if blockReceipts[index].Status != types.ReceiptStatusSuccessful {
				continue
			}

			switch true {
			// Check if it's a native ETH transfer
			case tx.To() != nil && tx.Value().Cmp(big.NewInt(0)) > 0:
				toAddr := strings.ToLower(tx.To().Hex())

				// Check if address exists in network addresses list
				if userId, ok := addressList[toAddr]; ok {
					// Get sender address
					from, err := types.Sender(types.LatestSignerForChainID(chainID), tx)
					if err != nil {
						log.Printf("[%s] Failed to get sender for tx '%s': %v", eth.NetworkCode, txHash, err)
						continue
					}

					// Get converted amount (in ETH)
					amount := utils.RemoveDecimals(tx.Value(), int64(ethDecimals))

					txList = append(txList, &InboundTransaction{
						UserId:   int(userId),
						Currency: "ETH",
						TxId:     txHash,
						From:     from.Hex(),
						To:       tx.To().Hex(),
						Amount:   amount,
					})
				}

			// Check if it's an ERC20 token transfer
			// Data structure: 4 bytes method ID + 32 bytes address + 32 bytes amount
			case tx.To() != nil && len(tx.Data()) >= 68:
				// Check if method is transfer (a9059cbb)
				methodId := fmt.Sprintf("%x", tx.Data()[0:4])
				if methodId == "a9059cbb" {
					// Get contract address
					contractAddr := strings.ToLower(tx.To().Hex())

					// Get token name by contract address
					token, err := config.GetTokenByContractAddr(eth.NetworkCode, contractAddr)
					if err != nil {
						continue
					}

					// Extract to address (bytes 4-36, take last 20 bytes)
					toAddrBytes := tx.Data()[16:36]
					toAddr := strings.ToLower(common.BytesToAddress(toAddrBytes).Hex())

					// Check if address exists in network addresses list
					if userId, ok := addressList[toAddr]; ok {
						// Trying to get contract decimals from cache
						eth.decimalsCacheMu.RLock()
						decimalsValue, exists := eth.decimalsCache[contractAddr]
						eth.decimalsCacheMu.RUnlock()
						// Fetch from contract
						if !exists {
							if err := limiter.Wait(ctx); err != nil {
								return nil, err
							}

							// Get contract instance from ABI
							var out []interface{}
							contract := bind.NewBoundContract(common.HexToAddress(contractAddr), parsedAbi, eth.Client, eth.Client, eth.Client)

							// Fetch decimals from contract
							err = contract.Call(&bind.CallOpts{}, &out, "decimals")
							if err != nil {
								log.Printf("[%s] Failed to get contract decimals for '%s': %v",
									eth.NetworkCode, contractAddr, err)
								continue
							}
							decimalsValue = *abi.ConvertType(out[0], new(int64)).(*int64)

							// Save decimals to cache
							eth.decimalsCacheMu.Lock()
							eth.decimalsCache[contractAddr] = decimalsValue
							eth.decimalsCacheMu.Unlock()
						}

						// Get sender address
						from, err := types.Sender(types.LatestSignerForChainID(chainID), tx)
						if err != nil {
							log.Printf("[%s] Failed to get sender for tx '%s': %v", eth.NetworkCode, txHash, err)
							continue
						}

						// Extract amount (bytes 36-68)
						rawAmount := new(big.Int).SetBytes(tx.Data()[36:68])
						// Get converted amount
						amount := utils.RemoveDecimals(rawAmount, decimalsValue)

						txList = append(txList, &InboundTransaction{
							UserId:   int(userId),
							Currency: token,
							TxId:     txHash,
							From:     from.Hex(),
							To:       common.BytesToAddress(toAddrBytes).Hex(),
							Amount:   amount,
						})
					}
				}
			}
		}

		// Save last processed block to Redis
		err = redis.GetClient().HSet(ctx, "last_blocks", eth.NetworkCode, blockNum)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save block %d to Redis: %w", eth.NetworkCode, blockNum, err)
		}
	}

	log.Printf("[%s] Processing finished successfully, found %d user transactions", eth.NetworkCode, len(txList))
	return txList, nil
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
	ethDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", eth.NetworkCode, "ETH"),
	)
	// Return converted balance (in ETH)
	return utils.RemoveDecimals(rawBalance, int64(ethDecimals)), nil
}

// @private getErc20Balance Get wallet ERC20 token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: ERC20 token wallet balance
func (eth *Ethereum) getErc20Balance(contractAddr, addr string) (float64, error) {
	// Get token ABI from config
	erc20Abi, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.abi", eth.NetworkCode),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get ABI from config: %w", err)
	}
	parsedAbi, err := abi.JSON(strings.NewReader(erc20Abi))
	if err != nil {
		return 0, fmt.Errorf("failed to parse ABI from config: %w", err)
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
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in ETH
func (eth *Ethereum) calcNativeFee(fromAddr, toAddr string, amount float64) (float64, error) {
	// Get ETH decimals from config
	weiDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", eth.NetworkCode, "ETH"),
	)
	// Convert amount to WEI
	amountWei := utils.ApplyDecimals(amount, int64(weiDecimals))

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
	// Return convert total fee in ETH
	return utils.RemoveDecimals(totalFeeWei, int64(weiDecimals)), nil
}

// @private calcErc20Fee Calc fee for ERC20 token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in ETH
func (eth *Ethereum) calcErc20Fee(currency, fromAddr, toAddr string, amount float64) (float64, error) {
	currency = strings.ToUpper(currency)

	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", eth.NetworkCode, currency),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get token ABI from config
	erc20Abi, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.abi", eth.NetworkCode),
	)
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
	// Get ETH decimals from config
	weiDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", eth.NetworkCode, "ETH"),
	)
	// Return converted total fee in ETH
	return utils.RemoveDecimals(totalFeeWei, int64(weiDecimals)), nil
}

/* SendTransaction private methods group */
// @private sendNative Send native ETH transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// privateKey: Wallet private key
// Returns: Transaction hash
func (eth *Ethereum) sendNative(fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	// Get ETH balance
	trxBalance, err := eth.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get ETH balance: %w", err)
	}
	// Get tx fee (in ETH)
	txFee, err := eth.calcNativeFee(fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get ETH tx fee: %w", err)
	}
	// Check ETH balance before sending
	if trxBalance < amount+txFee {
		return "", fmt.Errorf("insufficient balance: have %f %s, need %f + %f %s", trxBalance, "ETH", amount, txFee, "ETH")
	}

	// Get ETH decimals from config
	weiDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", eth.NetworkCode, "ETH"),
	)
	// Convert amount to WEI
	amountWei := utils.ApplyDecimals(amount, int64(weiDecimals))

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

	// Sign and send transaction to the network
	return eth.signAndSend(tx, privateKey)
}

// @private sendErc20 Send ERC20 token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// privateKey: Wallet private key
// Returns: Transaction hash
func (eth *Ethereum) sendErc20(currency, fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	currency = strings.ToUpper(currency)

	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", eth.NetworkCode, currency),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get ETH and token balances
	ethBalance, err := eth.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get ETH balance: %w", err)
	}
	erc20Balance, err := eth.getErc20Balance(contractAddr, fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get %s balance: %w", currency, err)
	}
	// Get tx fee (in ETH)
	txFee, err := eth.calcErc20Fee(currency, fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get %s tx fee: %w", currency, err)
	}
	// Check ETH and currency balances before sending
	if ethBalance < txFee {
		return "", fmt.Errorf("insufficient ETH balance: have %f %s, need %f %s", ethBalance, "ETH", txFee, "ETH")
	}
	if erc20Balance < amount {
		return "", fmt.Errorf("insufficient currency balance: have %f %s, need %f %s", erc20Balance, currency, amount, currency)
	}

	// Get token ABI from config
	erc20Abi, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.abi", eth.NetworkCode),
	)
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

	// Sign and send transaction to the network
	return eth.signAndSend(tx, privateKey)
}

// @private signAndSend Sign and send transaction to the network
// tx: Transaction struct
// privateKey: Wallet private key
// Returns: Transaction hash
func (eth *Ethereum) signAndSend(tx *types.Transaction, privateKey string) (string, error) {
	// Convert hex private key string to *ecdsa.PrivateKey
	ecdsaPk, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return "", fmt.Errorf("invalid or missing %s master private key in Vault", eth.NetworkCode)
	}

	// Get chain id
	chainId, err := eth.Client.NetworkID(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get chain ID: %w", err)
	}

	// Sign transaction (EIP-155)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainId), ecdsaPk)
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
