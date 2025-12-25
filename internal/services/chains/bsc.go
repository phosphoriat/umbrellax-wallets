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

// Bsc RPC connection struct
type Bsc struct {
	BaseChainService
	Client          *ethclient.Client
	RateLimit       int
	decimalsCache   map[string]int64
	decimalsCacheMu sync.RWMutex
}

// Global package vars
var (
	bscInstance *Bsc
	bscOnce     sync.Once
)

// GetBsc Get Bsc chain instance
func GetBsc() *Bsc {
	bscOnce.Do(func() {
		bscInstance = newBsc()
	})
	return bscInstance
}

// @private newBsc Create new instance of Bsc (private constructor)
func newBsc() *Bsc {
	// Load config
	cfg, _ := config.Load()

	// Create new RPC client
	bscClient, err := ethclient.Dial(cfg.BscRpcUrl + "/" + cfg.QuickNodeApiKey)
	if err != nil {
		log.Printf("[BSC] Failed to connect to RPC API: %v", err)
	}

	networkCode := "bsc"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	return &Bsc{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
		},
		Client: bscClient,
	}
}

// GetCode Get network code
func (bsc *Bsc) GetCode() string {
	return bsc.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
// userId: Unique user id
// Returns: User HD-wallet address
func (bsc *Bsc) GetUserWallet(userId int) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + bsc.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	// Derive user address from master private key
	derivedAddr, err := bsc.deriveAddress(masterData["privateKey"].(string), uint32(userId))
	if err != nil {
		return "", err
	}

	return derivedAddr, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of master wallet info (address and balances of all available tokens)
func (bsc *Bsc) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + bsc.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Get BEP20 token balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[bsc.NetworkCode].(map[string]string) {
		tokenBalance, _ := bsc.getBep20Balance(contractAddr, masterAddr)
		balances[token] = tokenBalance
	}

	// Get ETH native balance
	ethBalance, _ := bsc.getNativeBalance(masterAddr)
	balances[bsc.NativeToken] = ethBalance

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
func (bsc *Bsc) GetTransactionCommission(
	oType, wType string, wId *string, currency, toAddr string, amount float64, memo string,
) (map[string]float64, error) {
	var walletPath string
	if wType == "master" {
		walletPath = "/master/" + bsc.NetworkCode
	} else {
		if wId == nil {
			return nil, fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, bsc.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet address
	fromAddr, ok := walletData["address"].(string)
	if !ok || fromAddr == "" {
		return nil, fmt.Errorf("invalid or missing %s wallet address in Vault", bsc.NetworkCode)
	}

	// Calc chain fee for currency
	var chainFee float64 = 0
	switch currency {
	case "BNB":
		chainFee, err = bsc.calcNativeFee(fromAddr, toAddr, amount)
	default:
		chainFee, err = bsc.calcBep20Fee(currency, fromAddr, toAddr, amount)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction commission: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetHTTPClient().GetWithdrawalFee(currency, bsc.NetworkCode)

		// Get AML check cost from config (in USD value)
		cfg, _ := config.Load()
		amlFee, _ = strconv.ParseFloat(cfg.AmlCheckCost, 64)
	}

	return map[string]float64{
		"chain_fee": chainFee, // In BNB
		"bybit_fee": bybitFee, // In currency value
		"aml_fee":   amlFee,   // in USD
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status (success/pending/failed)
func (bsc *Bsc) GetTransactionStatus(hash, toAddr string) (string, error) {
	_, isPending, err := bsc.Client.TransactionByHash(context.Background(), common.HexToHash(hash))
	if err != nil {
		return "", fmt.Errorf("failed to get transaction %s: %w", hash, err)
	}
	if isPending {
		return "pending", nil
	}

	receipt, err := bsc.Client.TransactionReceipt(context.Background(), common.HexToHash(hash))
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
func (bsc *Bsc) SendTransaction(wType string, wId *string, currency, toAddr string, amount float64, memo string) (string, error) {
	var walletPath string
	if wType == "master" {
		walletPath = fmt.Sprintf("/master/%s", bsc.NetworkCode)
	} else {
		if wId == nil {
			return "", fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, bsc.NetworkCode)
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
		return "", fmt.Errorf("invalid or missing %s wallet data in Vault", bsc.NetworkCode)
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "BNB":
		hash, err = bsc.sendNative(fromAddr, toAddr, amount, privateKey)
	default:
		hash, err = bsc.sendBep20(currency, fromAddr, toAddr, amount, privateKey)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, nil
}

// GetInboundTransactions Get new inbound transactions list
func (bsc *Bsc) GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error) {
	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(bsc.RateLimit), 1)

	// Get current block number
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	currentBlockNum, err := bsc.Client.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get current block number: %w", bsc.NetworkCode, err)
	}

	// Get last block number from Redis (or use current block number, if not exists)
	lastBlock, err := redis.GetClient().HGet(ctx, "last_blocks", bsc.NetworkCode)
	var lastBlockNumber uint64
	if err != nil || lastBlock == "" {
		lastBlockNumber = currentBlockNum - 1
		log.Printf("[%s] First run, starting from block %d (current: %d)",
			bsc.NetworkCode, lastBlockNumber, currentBlockNum)
	} else {
		parsed, _ := strconv.ParseUint(lastBlock, 10, 64)
		lastBlockNumber = parsed
	}

	// Check if current block not equal or less, than last block
	if currentBlockNum <= lastBlockNumber {
		log.Printf("[%s] No new blocks to process", bsc.NetworkCode)
		return []*InboundTransaction{}, nil
	}

	// Get all address for network from DB
	addressList, err := bsc.WalletRepo.GetAddressListByNetwork(bsc.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get wallet address list: %w", bsc.NetworkCode, err)
	}

	// Get current chain id
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	chainID, err := bsc.Client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get chain ID: %w", bsc.NetworkCode, err)
	}

	// Get BNB decimals from config
	bnbDecimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", bsc.NetworkCode, "BNB"),
	)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get BNB decimals from config: %w", bsc.NetworkCode, err)
	}

	// Get token ABI from config
	bep20Abi, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.abi", bsc.NetworkCode),
	)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get BEP20 ABI from config: %w", bsc.NetworkCode, err)
	}
	parsedAbi, err := abi.JSON(strings.NewReader(bep20Abi))
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to parse BEP20 ABI from config: %w", bsc.NetworkCode, err)
	}

	log.Printf("[%s] Processing %d blocks from %d to %d",
		bsc.NetworkCode, currentBlockNum-lastBlockNumber, lastBlockNumber+1, currentBlockNum)

	// Transaction list for all blocks
	txList := make([]*InboundTransaction, 0)

	// Process all new blocks
	for blockNum := lastBlockNumber + 1; blockNum <= currentBlockNum; blockNum++ {
		// Get block entity by number
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
		block, err := bsc.Client.BlockByNumber(ctx, big.NewInt(int64(blockNum)))
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to get block %d: %w", bsc.NetworkCode, blockNum, err)
		}

		// Get all block receipts (for check status)
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
		blockReceipts, err := bsc.Client.BlockReceipts(ctx, rpc.BlockNumberOrHashWithHash(block.Hash(), true))
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to get block receipts: %w", bsc.NetworkCode, err)
		}

		log.Printf("[%s] Processing block %d with %d transactions",
			bsc.NetworkCode, blockNum, len(block.Transactions()))

		// Process transactions in block
		for index, tx := range block.Transactions() {
			// Get tx hash
			txHash := tx.Hash().Hex()
			// Skip, if transaction is not success
			if blockReceipts[index].Status != types.ReceiptStatusSuccessful {
				continue
			}

			switch true {
			// Check if it's a native BNB transfer
			case tx.To() != nil && tx.Value().Cmp(big.NewInt(0)) > 0:
				toAddr := strings.ToLower(tx.To().Hex())

				// Check if address exists in network addresses list
				if userId, ok := addressList[toAddr]; ok {
					// Get sender address
					from, err := types.Sender(types.LatestSignerForChainID(chainID), tx)
					if err != nil {
						log.Printf("[%s] Failed to get sender for tx '%s': %v", bsc.NetworkCode, txHash, err)
						continue
					}

					// Get converted amount (in BNB)
					amount := utils.RemoveDecimals(tx.Value(), int64(bnbDecimals))

					txList = append(txList, &InboundTransaction{
						UserId:   int(userId),
						Currency: "BNB",
						TxId:     txHash,
						From:     from.Hex(),
						To:       tx.To().Hex(),
						Amount:   amount,
					})
				}

			// Check if it's an BEP20 token transfer
			// Data structure: 4 bytes method ID + 32 bytes address + 32 bytes amount
			case tx.To() != nil && len(tx.Data()) >= 68:
				// Check if method is transfer (a9059cbb)
				methodId := fmt.Sprintf("%x", tx.Data()[0:4])
				if methodId == "a9059cbb" {
					// Get contract address
					contractAddr := strings.ToLower(tx.To().Hex())

					// Get token name by contract address
					token, err := config.GetTokenByContractAddr(bsc.NetworkCode, contractAddr)
					if err != nil {
						continue
					}

					// Extract to address (bytes 4-36, take last 20 bytes)
					toAddrBytes := tx.Data()[16:36]
					toAddr := strings.ToLower(common.BytesToAddress(toAddrBytes).Hex())

					// Check if address exists in network addresses list
					if userId, ok := addressList[toAddr]; ok {
						// Trying to get contract decimals from cache
						bsc.decimalsCacheMu.RLock()
						decimalsValue, exists := bsc.decimalsCache[contractAddr]
						bsc.decimalsCacheMu.RUnlock()
						// Fetch from contract
						if !exists {
							if err := limiter.Wait(ctx); err != nil {
								return nil, err
							}

							// Get contract instance from ABI
							var out []interface{}
							contract := bind.NewBoundContract(
								common.HexToAddress(contractAddr), parsedAbi, bsc.Client, bsc.Client, bsc.Client,
							)

							// Fetch decimals from contract
							err = contract.Call(&bind.CallOpts{}, &out, "decimals")
							if err != nil {
								log.Printf("[%s] Failed to get contract decimals for '%s': %v",
									bsc.NetworkCode, contractAddr, err)
								continue
							}
							decimalsValue = *abi.ConvertType(out[0], new(int64)).(*int64)

							// Save decimals to cache
							bsc.decimalsCacheMu.Lock()
							bsc.decimalsCache[contractAddr] = decimalsValue
							bsc.decimalsCacheMu.Unlock()
						}

						// Get sender address
						from, err := types.Sender(types.LatestSignerForChainID(chainID), tx)
						if err != nil {
							log.Printf("[%s] Failed to get sender for tx '%s': %v",
								bsc.NetworkCode, txHash, err)
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
		err = redis.GetClient().HSet(ctx, "last_blocks", bsc.NetworkCode, blockNum)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save block %d to Redis: %w", bsc.NetworkCode, blockNum, err)
		}
	}

	log.Printf("[%s] Processing finished successfully, found %d user transactions", bsc.NetworkCode, len(txList))
	return txList, nil
}

/* GetUserWallet private methods group */
// @private deriveAddress Derive HD-wallet address by derivation index from master wallet private key
// masterPrivateKey: Master wallet private key
// userId: Unique user id for derivation index
// Returns: Derived address
func (bsc *Bsc) deriveAddress(masterPrivateKey string, userId uint32) (string, error) {
	// Get bytes from private key
	bytes, _ := hex.DecodeString(masterPrivateKey)
	// Create new master node from bytes for use in creating HD keychain
	masterHdKey, err := hdkeychain.NewMaster(bytes, &chaincfg.MainNetParams)
	if err != nil {
		return "", fmt.Errorf("failed to derive master key: %w", err)
	}

	// BIP44 derivation: m/44'/60'/1'/0/{index} (accountKey = 1)
	purposeKey, _ := masterHdKey.Derive(hdkeychain.HardenedKeyStart + 44)
	coinKey, _ := purposeKey.Derive(hdkeychain.HardenedKeyStart + 60)
	accountKey, _ := coinKey.Derive(hdkeychain.HardenedKeyStart + 1)
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
// @private getNativeBalance Get wallet BNB balance
// addr: Wallet address
// Returns: BNB wallet balance
func (bsc *Bsc) getNativeBalance(addr string) (float64, error) {
	// Get ethereum address instance
	bnbAddr := common.HexToAddress(addr)

	// Get raw balance (in WEI)
	rawBalance, err := bsc.Client.BalanceAt(context.Background(), bnbAddr, nil)
	if err != nil {
		return 0, err
	}

	// Get BNB decimals from config
	bnbDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", bsc.NetworkCode, "BNB"),
	)
	// Return converted balance (in BNB)
	return utils.RemoveDecimals(rawBalance, int64(bnbDecimals)), nil
}

// @private getBep20Balance Get wallet BEP20 token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: BEP20 token wallet balance
func (bsc *Bsc) getBep20Balance(contractAddr, addr string) (float64, error) {
	// Get token ABI from config
	bep20Abi, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.abi", bsc.NetworkCode),
	)
	parsedAbi, err := abi.JSON(strings.NewReader(bep20Abi))
	if err != nil {
		return 0, fmt.Errorf("failed to parse ABI: %w", err)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), parsedAbi, bsc.Client, bsc.Client, bsc.Client)

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
// @private calcNativeFee Calc fee for BNB transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in BNB
func (bsc *Bsc) calcNativeFee(fromAddr, toAddr string, amount float64) (float64, error) {
	// Get BNB decimals from config
	weiDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", bsc.NetworkCode, "BNB"),
	)
	// Convert amount to WEI
	amountWei := utils.ApplyDecimals(amount, int64(weiDecimals))

	// Get converted from and to addresses
	from := common.HexToAddress(fromAddr)
	to := common.HexToAddress(toAddr)

	// Get estimated gas limit
	gasLimit, err := bsc.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  from,
		To:    &to,
		Value: amountWei,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get estimate gas limit: %w", err)
	}

	// Get current gas price
	gasPrice, err := bsc.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get current gas price: %w", err)
	}

	// Get total fee in WEI
	totalFeeWei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	// Return converted total fee in BNB
	return utils.RemoveDecimals(totalFeeWei, int64(weiDecimals)), nil
}

// @private calcBep20Fee Calc fee for BEP20 token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in BNB
func (bsc *Bsc) calcBep20Fee(currency, fromAddr, toAddr string, amount float64) (float64, error) {
	currency = strings.ToUpper(currency)

	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", bsc.NetworkCode, currency),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get token ABI from config
	bep20Abi, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.abi", bsc.NetworkCode),
	)
	parsedAbi, err := abi.JSON(strings.NewReader(bep20Abi))
	if err != nil {
		return 0, fmt.Errorf("failed to parse ABI: %w", err)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), parsedAbi, bsc.Client, bsc.Client, bsc.Client)

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
	gasLimit, err := bsc.Client.EstimateGas(context.Background(), ethereum.CallMsg{
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
	gasPrice, err := bsc.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Get total fee in WEI
	totalFeeWei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	// Get BNB decimals from config
	weiDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", bsc.NetworkCode, "BNB"),
	)

	// Return converted total fee in BNB
	return utils.RemoveDecimals(totalFeeWei, int64(weiDecimals)), nil
}

/* SendTransaction private methods group */
// @private sendNative Send native BNB transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// privateKey: Wallet private key
// Returns: Transaction hash
func (bsc *Bsc) sendNative(fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	// Get BNB balance
	bnbBalance, err := bsc.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get BNB balance: %w", err)
	}
	// Get tx fee (in BNB)
	txFee, err := bsc.calcNativeFee(fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get BNB tx fee: %w", err)
	}
	// Check BNB balance before sending
	if bnbBalance < amount+txFee {
		return "", fmt.Errorf("insufficient balance: have %f %s, need %f + %f %s", bnbBalance, "BNB", amount, txFee, "BNB")
	}

	// Get BNB decimals from config
	weiDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", bsc.NetworkCode, "BNB"),
	)
	// Convert amount to WEI
	amountWei := utils.ApplyDecimals(amount, int64(weiDecimals))

	// Get converted from and to address
	from := common.HexToAddress(fromAddr)
	to := common.HexToAddress(toAddr)

	// Get estimated gas limit
	gasLimit, err := bsc.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  from,
		To:    &to,
		Value: amountWei,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get estimate gas limit: %w", err)
	}

	// Get current gas price
	gasPrice, err := bsc.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get current gas price: %w", err)
	}

	// Get nonce for sender
	nonce, err := bsc.Client.PendingNonceAt(context.Background(), common.HexToAddress(fromAddr))
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
	return bsc.signAndSend(tx, privateKey)
}

// @private sendBep20 Send BEP20 token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// privateKey: Wallet private key
// Returns: Transaction hash
func (bsc *Bsc) sendBep20(currency, fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	currency = strings.ToUpper(currency)

	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", bsc.NetworkCode, currency),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get BNB and token balances
	bnbBalance, err := bsc.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get BNB balance: %w", err)
	}
	bep20Balance, err := bsc.getBep20Balance(contractAddr, fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get %s balance: %w", currency, err)
	}
	// Get tx fee (in BNB)
	txFee, err := bsc.calcBep20Fee(currency, fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get %s tx fee: %w", currency, err)
	}
	// Check BNB and currency balances before sending
	if bnbBalance < txFee {
		return "", fmt.Errorf("insufficient BNB balance: have %f %s, need %f %s", bnbBalance, "BNB", txFee, "BNB")
	}
	if bep20Balance < amount {
		return "", fmt.Errorf("insufficient currency balance: have %f %s, need %f %s", bep20Balance, currency, amount, currency)
	}

	// Get token ABI from config
	bep20Abi, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.abi", bsc.NetworkCode),
	)
	parsedAbi, err := abi.JSON(strings.NewReader(bep20Abi))
	if err != nil {
		return "", fmt.Errorf("failed to parse ABI: %w", err)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), parsedAbi, bsc.Client, bsc.Client, bsc.Client)

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
	gasLimit, err := bsc.Client.EstimateGas(context.Background(), ethereum.CallMsg{
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
	gasPrice, err := bsc.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get current gas price: %w", err)
	}

	// Get nonce for sender
	nonce, err := bsc.Client.PendingNonceAt(context.Background(), common.HexToAddress(fromAddr))
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
	return bsc.signAndSend(tx, privateKey)
}

// @private signAndSend Sign and send transaction to the network
// tx: Transaction struct
// privateKey: Wallet private key
// Returns: Transaction hash
func (bsc *Bsc) signAndSend(tx *types.Transaction, privateKey string) (string, error) {
	// Convert hex private key string to *ecdsa.PrivateKey
	ecdsaPk, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return "", fmt.Errorf("invalid or missing %s master private key in Vault", bsc.NetworkCode)
	}

	// Get chain id
	chainId, err := bsc.Client.NetworkID(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get chain ID: %w", err)
	}

	// Sign transaction (EIP-155)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainId), ecdsaPk)
	if err != nil {
		return "", fmt.Errorf("failed to sign tx: %w", err)
	}

	// Send transaction
	if err := bsc.Client.SendTransaction(context.Background(), signedTx); err != nil {
		return "", fmt.Errorf("failed to send tx: %w", err)
	}

	// Return transaction hash
	return signedTx.Hash().Hex(), nil
}
