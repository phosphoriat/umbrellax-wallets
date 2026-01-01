package chains

import (
	"context"
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
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
)

type Ethereum struct {
	BaseChainService
	Client          *ethclient.Client
	RateLimit       int
	decimalsCache   map[string]int64
	decimalsCacheMu sync.RWMutex
	ABI             abi.ABI
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

	// Get network settings
	networkCode := "ethereum"
	nativeToken := config.NetworkSettings[networkCode].NativeToken

	// Get ABI from config
	abiString := config.NetworkSettings[networkCode].ABI
	// Parse ABI from config
	parsedAbi, err := abi.JSON(strings.NewReader(abiString))
	if err != nil {
		log.Fatalf("[%s] Failed to parse ABI from network settings: %v", networkCode, err)
	}

	return &Ethereum{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: nativeToken,
		},
		Client:        ethClient,
		RateLimit:     cfg.QuickNodeRps,
		decimalsCache: make(map[string]int64),
		ABI:           parsedAbi,
	}
}

// GetCode Get network code
// Returns: Network code
func (eth *Ethereum) GetCode() string {
	return eth.NetworkCode
}

// GetUserWalletAddress Get user HD-wallet by user id
// userId: Unique user id
// Returns: User HD-wallet address
func (eth *Ethereum) GetUserWalletAddress(userId uint32) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + eth.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}
	mnemonic, ok := masterData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return "", fmt.Errorf("mnemonic not found in Vault")
	}

	// Derive user address from master mnemonic
	addr, _, err := eth.deriveWallet(mnemonic, userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive address for user %d: %w", userId, err)
	}

	return addr, nil
}

// GetUserWallet Get user wallet info (address and token balances)
// userId: Unique user id
// Returns: Map of user wallet info (address and balances of all available tokens)
func (eth *Ethereum) GetUserWallet(userId uint32) (map[string]any, error) {
	// Get user wallet address
	userAddr, _, err := eth.getWallet("user", &userId)
	if err != nil {
		return nil, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	// Get ERC20 token balances
	balances := make(map[string]float64)
	for token, contractAddr := range config.ContractsAddresses[eth.NetworkCode] {
		tokenBalance, err := eth.getErc20Balance(contractAddr, userAddr)
		if err != nil {
			continue
		}

		balances[token] = tokenBalance
	}

	// Get ETH native balance
	ethBalance, err := eth.getNativeBalance(userAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance: %w", err)
	}
	balances[eth.NativeToken] = ethBalance

	return map[string]any{
		"address":  userAddr,
		"balances": balances,
	}, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of master wallet info (address and balances of all available tokens)
func (eth *Ethereum) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + eth.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("master address not found in Vault")
	}

	// Get ERC20 token balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[eth.NetworkCode] {
		tokenBalance, err := eth.getErc20Balance(contractAddr, masterAddr)
		if err != nil {
			continue
		}

		balances[token] = tokenBalance
	}

	// Get ETH native balance
	ethBalance, err := eth.getNativeBalance(masterAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance: %w", err)
	}
	balances[eth.NativeToken] = ethBalance

	return map[string]any{
		"address":  masterAddr,
		"balances": balances,
	}, nil
}

// GetUserWalletBalance Get token balance amount for user wallet
// userId: User id
// currency: Token ticker
// Returns Balance amount for token
func (eth *Ethereum) GetUserWalletBalance(userId uint32, currency string) (float64, error) {
	// Get user wallet address
	userAddr, _, err := eth.getWallet("user", &userId)
	if err != nil {
		return 0, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	var balance float64
	if currency == "ETH" {
		balance, err = eth.getNativeBalance(userAddr)
	} else {
		contractAddr, ok := config.ContractsAddresses[eth.NetworkCode][currency]
		if !ok {
			return 0, fmt.Errorf("unsupported currency %q on network %s", currency, eth.NetworkCode)
		}

		balance, err = eth.getErc20Balance(contractAddr, userAddr)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get balance for user %d: %w", userId, err)
	}

	return balance, nil
}

// GetTransactionCommission Get transaction commission
// oType: Operation type (withdrawal/inner)
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// Returns: Map of all fee types (chain_fee, bybit_fee, aml_fee)
func (eth *Ethereum) GetTransactionCommission(oType, wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (map[string]float64, error) {
	// Get wallet address by type and id
	walletAddr, _, err := eth.getWallet(wType, wId)
	if err != nil {
		return nil, err
	}

	// Calc chain fee for currency (in ETH value)
	var chainFee float64 = 0
	switch currency {
	case "ETH":
		chainFee, err = eth.calcNativeFee()
	default:
		chainFee, err = eth.calcErc20Fee(currency, walletAddr, toAddr, amount)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction commission: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Load config
		cfg, _ := config.Load()

		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetClient().GetWithdrawalFee(context.Background(), currency, eth.NetworkCode)
		// Get AML check cost from config (in USD value)
		amlFee = cfg.AmlCheckCost
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
func (eth *Ethereum) GetTransactionStatus(hash, fromAddr string) (string, error) {
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
func (eth *Ethereum) SendTransaction(wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (string, error) {
	// Get wallet address and private key by type and id
	walletAddr, walletKey, err := eth.getWallet(wType, wId)
	if err != nil {
		return "", err
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "ETH":
		hash, err = eth.sendNative(walletAddr, toAddr, amount, walletKey)
	default:
		hash, err = eth.sendErc20(currency, walletAddr, toAddr, amount, walletKey)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, nil
}

// GetInboundTransactions Get new inbound transactions list
// Returns: Inbound transactions list
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
	ethDecimals := config.TokenSettings[eth.NetworkCode]["ETH"].Decimals

	log.Printf("[%s] Processing %d blocks from %d to %d",
		eth.NetworkCode, currentBlockNum-lastBlockNumber, lastBlockNumber+1, currentBlockNum)

	var (
		// Wait group to wait for all tx blocks goroutines to complete
		wg sync.WaitGroup
		// Semaphore for goroutines limit
		sem = semaphore.NewWeighted(20)
		// Concurrent save map for successfully processed blocks
		successfulBlocks = &sync.Map{}
		// Channel for collect transactions from all blocks
		resultChan = make(chan []*InboundTransaction, currentBlockNum-lastBlockNumber)
		// Goroutines cancellation context
		cancelCtx, cancel = context.WithCancel(ctx)
		// First error flag
		errOnce  sync.Once
		firstErr error
	)
	defer cancel()

	// Process all new blocks in separate goroutines
	for blockNum := lastBlockNumber + 1; blockNum <= currentBlockNum; blockNum++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			// Check context cancellation
			if cancelCtx.Err() != nil {
				return
			}

			// Parallel execution limit
			if err := sem.Acquire(cancelCtx, 1); err != nil {
				return
			}
			defer sem.Release(1)

			// Get block entity by number
			if err := limiter.Wait(cancelCtx); err != nil {
				log.Printf("[%s] Rate limit error for block %d: %v", eth.NetworkCode, blockNum, err)
				return
			}
			block, err := eth.Client.BlockByNumber(cancelCtx, big.NewInt(int64(blockNum)))
			if err != nil {
				log.Printf("[%s] Failed to get block %d: %v", eth.NetworkCode, blockNum, err)
				// Cancel all another goroutines
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			// Get all block receipts (for check status)
			if err := limiter.Wait(cancelCtx); err != nil {
				log.Printf("[%s] Rate limit error for block %d: %v", eth.NetworkCode, blockNum, err)
				return
			}
			blockReceipts, err := eth.Client.BlockReceipts(cancelCtx, rpc.BlockNumberOrHashWithHash(block.Hash(), true))
			if err != nil {
				log.Printf("[%s] Failed to get block receipts: %v", eth.NetworkCode, err)
				// Cancel all another goroutines
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			log.Printf("[%s] Processing block %d with %d raw txs",
				eth.NetworkCode, blockNum, len(block.Transactions()))

			// Local goroutine tx list
			localTxList := make([]*InboundTransaction, 0)

			// Process transactions in block
			for index, tx := range block.Transactions() {
				// Get tx hash
				txHash := tx.Hash().Hex()
				// Skip, if transaction is not success
				if blockReceipts[index].Status != types.ReceiptStatusSuccessful {
					continue
				}

				switch true {
				// If it's a native ETH transfer
				case tx.To() != nil && tx.Value().Cmp(big.NewInt(0)) > 0:
					// Get to address
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

						localTxList = append(localTxList, &InboundTransaction{
							UserId:   userId,
							Currency: "ETH",
							TxId:     txHash,
							From:     strings.ToLower(from.Hex()),
							To:       toAddr,
							Amount:   amount,
						})
					}

				// If it's an ERC20 token transfer
				// Data structure: 4 bytes method ID + 32 bytes address + 32 bytes amount
				case tx.To() != nil && len(tx.Data()) >= 68:
					// Check if method is transfer (a9059cbb)
					methodId := fmt.Sprintf("%x", tx.Data()[0:4])
					if methodId == "a9059cbb" {
						// Get contract address
						contractAddr := strings.ToLower(tx.To().Hex())

						// Get token name by contract address
						token, ok := config.GetTokenByContractAddr(eth.NetworkCode, contractAddr)
						if !ok {
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
								if err := limiter.Wait(cancelCtx); err != nil {
									return
								}

								// Get contract instance from ABI
								var out []interface{}
								contract := bind.NewBoundContract(common.HexToAddress(contractAddr), eth.ABI, eth.Client, eth.Client, eth.Client)

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

							localTxList = append(localTxList, &InboundTransaction{
								UserId:   userId,
								Currency: token,
								TxId:     txHash,
								From:     strings.ToLower(from.Hex()),
								To:       toAddr,
								Amount:   amount,
							})
						}
					}
				}
			}

			// Mark the block as successfully processed
			successfulBlocks.Store(blockNum, true)
			// Send batch to result channel (one send for block)
			resultChan <- localTxList
		}()
	}

	// Waiting for results
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results from batches
	txList := make([]*InboundTransaction, 0)
	for batch := range resultChan {
		txList = append(txList, batch...)
	}

	// Get the last continuously successful block
	// This ensures that we don't miss blocks the next time we run
	lastContinuousBlock := lastBlockNumber
	for blockNum := lastBlockNumber + 1; blockNum <= currentBlockNum; blockNum++ {
		if _, ok := successfulBlocks.Load(blockNum); ok {
			lastContinuousBlock = blockNum
		} else {
			// Stop, if found the first failed block
			log.Printf("[%s] Block %d was not processed successfully, stopping at %d: %v",
				eth.NetworkCode, blockNum, lastContinuousBlock, firstErr)
			break
		}
	}

	// Save last processed block in Redis
	if lastContinuousBlock > lastBlockNumber {
		err = redis.GetClient().HSet(ctx, "last_blocks", eth.NetworkCode, lastContinuousBlock)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save last block in Redis: %w", eth.NetworkCode, err)
		}
	}

	log.Printf("[%s] Searching finished successfully: processed %d blocks, found %d user raw txs",
		eth.NetworkCode, lastContinuousBlock-lastBlockNumber, len(txList))
	return txList, nil
}

/* GetUserWallet private methods group */
// @private getWallet Get HD-wallet by index
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// Returns: Derived address and private key
func (eth *Ethereum) getWallet(wType string, wId *uint32) (string, string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + eth.NetworkCode)
	if err != nil {
		return "", "", fmt.Errorf("failed to get master data: %w", err)
	}
	mnemonic, ok := masterData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return "", "", fmt.Errorf("mnemonic not found in vault")
	}

	// Derive wallet address and private key from master mnemonic
	var index uint32
	if wType == "master" {
		index = 0
	} else {
		if wId == nil {
			return "", "", fmt.Errorf("user id is required")
		}
		index = *wId
	}

	return eth.deriveWallet(mnemonic, index)
}

// @private deriveWallet Derive HD-wallet by derivation index from master wallet mnemonic
// mnemonic: Master wallet mnemonic
// userId: Unique user id for derivation index
// Returns: Derived address and private key
func (eth *Ethereum) deriveWallet(mnemonic string, userId uint32) (string, string, error) {
	// Validate mnemonic
	if !bip39.IsMnemonicValid(mnemonic) {
		return "", "", fmt.Errorf("invalid master wallet mnemonic")
	}
	// Generate seed from mnemonic (BIP39)
	seed := bip39.NewSeed(mnemonic, "")

	// Create HD master key from seed
	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return "", "", fmt.Errorf("failed to derive master HD key: %w", err)
	}

	// BIP44 derivation path: m/44'/60'/0'/0/{index} (accountKey = 0)
	path := []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 60,
		hdkeychain.HardenedKeyStart + 0,
		0,
		userId,
	}
	key := masterKey
	for _, idx := range path {
		key, err = key.Derive(idx)
		if err != nil {
			return "", "", fmt.Errorf("failed to derive key: %w", err)
		}
	}

	// Get private key
	btcecPrivKey, err := key.ECPrivKey()
	if err != nil {
		return "", "", fmt.Errorf("failed to get EC private key: %w", err)
	}
	ecdsaPrivKey := btcecPrivKey.ToECDSA()
	privateKey := hex.EncodeToString(crypto.FromECDSA(ecdsaPrivKey))

	// Get address from ECDSA public key
	address := crypto.PubkeyToAddress(ecdsaPrivKey.PublicKey).Hex()
	return address, privateKey, nil
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
	ethDecimals := config.TokenSettings[eth.NetworkCode]["ETH"].Decimals
	// Return converted balance (in ETH)
	return utils.RemoveDecimals(rawBalance, int64(ethDecimals)), nil
}

// @private getErc20Balance Get wallet ERC20 token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: ERC20 token wallet balance
func (eth *Ethereum) getErc20Balance(contractAddr, addr string) (float64, error) {
	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), eth.ABI, eth.Client, eth.Client, eth.Client)

	// Get raw balance (in decimal)
	err := contract.Call(&bind.CallOpts{}, &out, "balanceOf", common.HexToAddress(addr))
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
// Returns: Fee in ETH
func (eth *Ethereum) calcNativeFee() (float64, error) {
	// Get ETH decimals from config
	ethDecimals := config.TokenSettings[eth.NetworkCode]["ETH"].Decimals

	// Gas for native transfers is always 21000 (EVM level constant)
	gasLimit := uint64(21000)

	// Get current gas price
	gasPrice, err := eth.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get current gas price: %w", err)
	}

	// Get total fee in WEI
	totalFeeWei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	// Return convert total fee in ETH
	return utils.RemoveDecimals(totalFeeWei, int64(ethDecimals)), nil
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
	contractAddr, ok := config.GetContractAddress(eth.NetworkCode, currency)
	if !ok {
		return 0, fmt.Errorf("contract address not found in config: %s/%s", eth.NetworkCode, currency)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), eth.ABI, eth.Client, eth.Client, eth.Client)

	// Get contract decimals
	err := contract.Call(&bind.CallOpts{}, &out, "decimals")
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}
	decimalsValue := int64(*abi.ConvertType(out[0], new(uint8)).(*uint8))
	// Convert amount to WEI with contract decimals
	amountWei := utils.ApplyDecimals(amount, decimalsValue)

	// Get of converted to address and contract address
	to := common.HexToAddress(toAddr)
	contractAddress := common.HexToAddress(contractAddr)
	// Set params for transfer contract call
	data, err := eth.ABI.Pack("transfer", to, amountWei)

	// Try to estimate gas limit, fallback to 65000
	cfg, _ := config.GetNetworkConfig(eth.NetworkCode)
	gasLimit, err := eth.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  common.HexToAddress(fromAddr),
		To:    &contractAddress,
		Value: big.NewInt(0),
		Data:  data,
	})
	if err != nil {
		gasLimit = uint64(cfg.GasLimit)
	} else {
		gasLimit = gasLimit + (gasLimit * uint64(cfg.FeeBuffer)) // +10% buffer
	}

	// Get current gas price
	gasPrice, err := eth.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Get total fee in WEI
	totalFeeWei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	// Get ETH decimals from config
	ethDecimals := config.TokenSettings[eth.NetworkCode]["ETH"].Decimals
	// Return converted total fee in ETH
	return utils.RemoveDecimals(totalFeeWei, int64(ethDecimals)), nil
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
	txFee, err := eth.calcNativeFee()
	if err != nil {
		return "", fmt.Errorf("failed to get ETH tx fee: %w", err)
	}
	// Check ETH balance before sending
	if trxBalance < amount+txFee {
		return "", fmt.Errorf("insufficient balance: have %f %s, need %f + %f %s", trxBalance, "ETH", amount, txFee, "ETH")
	}

	// Get ETH decimals from config
	ethDecimals := config.TokenSettings[eth.NetworkCode]["ETH"].Decimals
	// Convert amount to WEI
	amountWei := utils.ApplyDecimals(amount, int64(ethDecimals))

	// Get converted to address
	to := common.HexToAddress(toAddr)

	// Gas for native transfers is always 21000 (EVM level constant)
	gasLimit := uint64(21000)

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
	contractAddr, ok := config.GetContractAddress(eth.NetworkCode, currency)
	if !ok {
		return "", fmt.Errorf("contract address not found in config: %s/%s", eth.NetworkCode, currency)
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
		return "", fmt.Errorf("insufficient ETH balance: have %f %s, need %f %s",
			ethBalance, "ETH", txFee, "ETH")
	}
	if erc20Balance < amount {
		return "", fmt.Errorf("insufficient currency balance: have %f %s, need %f %s",
			erc20Balance, currency, amount, currency)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), eth.ABI, eth.Client, eth.Client, eth.Client)

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
	data, err := eth.ABI.Pack("transfer", to, amountWei)

	// Try to estimate gas limit, fallback to 65000
	cfg, _ := config.GetNetworkConfig(eth.NetworkCode)
	gasLimit, err := eth.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  common.HexToAddress(fromAddr),
		To:    &contractAddress,
		Value: big.NewInt(0),
		Data:  data,
	})
	if err != nil {
		gasLimit = uint64(cfg.GasLimit)
	} else {
		gasLimit = gasLimit + (gasLimit / uint64(cfg.FeeBuffer)) // +10% buffer
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
