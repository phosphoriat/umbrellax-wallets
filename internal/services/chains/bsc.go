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
	"wallets/internal/services/api/notifications"
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

type Bsc struct {
	BaseChainService
	Client          *ethclient.Client
	RateLimit       int
	decimalsCache   map[string]int64
	decimalsCacheMu sync.RWMutex
	ABI             abi.ABI
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

	// Get network settings
	networkCode := "bsc"
	nativeToken := config.NetworkSettings[networkCode].NativeToken

	// Get ABI from config
	abiString := config.NetworkSettings[networkCode].ABI
	// Parse ABI from config
	parsedAbi, err := abi.JSON(strings.NewReader(abiString))
	if err != nil {
		log.Fatalf("[%s] Failed to parse ABI from network settings: %v", networkCode, err)
	}

	return &Bsc{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: nativeToken,
		},
		Client:        bscClient,
		RateLimit:     cfg.QuickNodeRps,
		decimalsCache: make(map[string]int64),
		ABI:           parsedAbi,
	}
}

// GetCode Get network code
func (bsc *Bsc) GetCode() string {
	return bsc.NetworkCode
}

// GetUserWalletAddress Get user HD-wallet by user id
// userId: Unique user id
// Returns: User HD-wallet address
func (bsc *Bsc) GetUserWalletAddress(userId uint32) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + bsc.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}
	mnemonic, ok := masterData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return "", fmt.Errorf("mnemonic not found in Vault")
	}

	// Derive user address from master mnemonic
	addr, _, err := bsc.deriveWallet(mnemonic, userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive address for user %d: %w", userId, err)
	}

	return strings.ToLower(addr), nil
}

// GetUserWallet Get user wallet info (address and token balances)
// userId: Unique user id
// Returns: Map of user wallet info (address and balances of all available tokens)
func (bsc *Bsc) GetUserWallet(userId uint32) (map[string]any, error) {
	// Get user wallet address
	userAddr, _, err := bsc.getWallet("user", &userId)
	if err != nil {
		return nil, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	// Get BEP20 token balances
	balances := make(map[string]float64)
	for token, contractAddr := range config.ContractsAddresses[bsc.NetworkCode] {
		tokenBalance, err := bsc.getBep20Balance(contractAddr, userAddr)
		if err != nil {
			continue
		}

		balances[token] = tokenBalance
	}

	// Get BNB native balance
	bnbBalance, err := bsc.getNativeBalance(userAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance: %w", err)
	}
	balances[bsc.NativeToken] = bnbBalance

	return map[string]any{
		"address":  userAddr,
		"balances": balances,
	}, nil
}

// GetUserWalletBalance Get token balance amount for user wallet
// userId: User id
// currency: Token ticker
// Returns Balance amount for token
func (bsc *Bsc) GetUserWalletBalance(userId uint32, currency string) (float64, error) {
	// Get user wallet address
	userAddr, _, err := bsc.getWallet("user", &userId)
	if err != nil {
		return 0, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	var balance float64
	if currency == "BNB" {
		balance, err = bsc.getNativeBalance(userAddr)
	} else {
		contractAddr, ok := config.ContractsAddresses[bsc.NetworkCode][currency]
		if !ok {
			return 0, fmt.Errorf("unsupported currency %q on network %s", currency, bsc.NetworkCode)
		}

		balance, err = bsc.getBep20Balance(contractAddr, userAddr)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get balance for user %d: %w", userId, err)
	}

	return balance, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of master wallet info (address and balances of all available tokens)
func (bsc *Bsc) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + bsc.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("master address not found in Vault")
	}

	// Get BEP20 token balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[bsc.NetworkCode] {
		tokenBalance, err := bsc.getBep20Balance(contractAddr, masterAddr)
		if err != nil {
			continue
		}

		balances[token] = tokenBalance
	}

	// Get BNB native balance
	bnbBalance, err := bsc.getNativeBalance(masterAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance: %w", err)
	}
	balances[bsc.NativeToken] = bnbBalance

	return map[string]any{
		"address":  masterAddr,
		"balances": balances,
	}, nil
}

// GetMasterWalletBalance Get token balance amount for master wallet
// currency: Token ticker
// Returns Balance amount for token
func (bsc *Bsc) GetMasterWalletBalance(currency string) (float64, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + bsc.NetworkCode)
	if err != nil {
		return 0, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return 0, fmt.Errorf("master address not found in Vault")
	}

	var balance float64
	if currency == "BNB" {
		balance, err = bsc.getNativeBalance(masterAddr)
	} else {
		contractAddr, ok := config.ContractsAddresses[bsc.NetworkCode][currency]
		if !ok {
			return 0, fmt.Errorf("unsupported currency %q on network %s", currency, bsc.NetworkCode)
		}

		balance, err = bsc.getBep20Balance(contractAddr, masterAddr)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get %s balance for %s master wallet: %w", currency, bsc.NetworkCode, err)
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
func (bsc *Bsc) GetTransactionCommission(oType, wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (map[string]float64, error) {
	// Get wallet address by type and id
	walletAddr, _, err := bsc.getWallet(wType, wId)
	if err != nil {
		return nil, err
	}

	// Calc chain fee for currency
	var chainFee float64 = 0
	switch currency {
	case "BNB":
		chainFee, err = bsc.calcNativeFee()
	default:
		chainFee, err = bsc.calcBep20Fee(currency, walletAddr, toAddr, amount)
	}
	if err != nil {
		return nil, err
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Load config
		cfg, _ := config.Load()

		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetClient().GetWithdrawalFee(context.Background(), currency, bsc.NetworkCode)
		// Get AML check cost from config (in USD value)
		amlFee = cfg.AmlCheckCost
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
func (bsc *Bsc) GetTransactionStatus(hash, fromAddr string) (string, error) {
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
func (bsc *Bsc) SendTransaction(wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (string, error) {
	// Get wallet address and private key by type and id
	walletAddr, walletKey, err := bsc.getWallet(wType, wId)
	if err != nil {
		return "", err
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "BNB":
		hash, err = bsc.sendNative(walletAddr, toAddr, amount, walletKey)
	default:
		hash, err = bsc.sendBep20(currency, walletAddr, toAddr, amount, walletKey)
	}

	if err != nil {
		// Send notifications to channels
		wIdPart := ""
		if wType == "user" {
			wIdPart = fmt.Sprintf(" [ID: %d]", wId)
		}
		errMsg := fmt.Sprintf("[SendTransaction] [%s] failed to send transaction (%f %s) from %s%s to %s: %v",
			bsc.NetworkCode, amount, currency, wType, wIdPart, toAddr, err)
		errSend := notifications.GetClient().SendNotifications(context.Background(), errMsg)
		if errSend != nil {
			return "", errSend
		}

		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, nil
}

// GetInboundTransactions Get new inbound transactions list
// ctx: Current process context
// Returns: Inbound transactions list
func (bsc *Bsc) GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error) {
	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(bsc.RateLimit), 1)

	// Get master wallet for network
	masterData, err := vault.GetClient().GetSecret("master/" + bsc.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get master data: %w", bsc.NetworkCode, err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("[%s] Master address not found in Vault", bsc.NetworkCode)
	}
	masterAddr = strings.ToLower(masterAddr)

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
	bnbDecimals := config.TokenSettings[bsc.NetworkCode]["BNB"].Decimals

	log.Printf("[%s] Processing %d blocks from %d to %d",
		bsc.NetworkCode, currentBlockNum-lastBlockNumber, lastBlockNumber+1, currentBlockNum)

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
				log.Printf("[%s] Rate limit error for block %d: %v", bsc.NetworkCode, blockNum, err)
				return
			}
			block, err := bsc.Client.BlockByNumber(cancelCtx, big.NewInt(int64(blockNum)))
			if err != nil {
				log.Printf("[%s] Failed to get block %d: %v", bsc.NetworkCode, blockNum, err)
				// Cancel all another goroutines
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			// Get all block receipts (for check status)
			if err := limiter.Wait(cancelCtx); err != nil {
				log.Printf("[%s] Rate limit error for block %d: %v", bsc.NetworkCode, blockNum, err)
				return
			}
			blockReceipts, err := bsc.Client.BlockReceipts(cancelCtx, rpc.BlockNumberOrHashWithHash(block.Hash(), true))
			if err != nil {
				log.Printf("[%s] Failed to get block receipts: %v", bsc.NetworkCode, err)
				// Cancel all another goroutines
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			log.Printf("[%s] Processing block %d with %d raw txs",
				bsc.NetworkCode, blockNum, len(block.Transactions()))

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
				// If it's a native BNB transfer
				case tx.To() != nil && tx.Value().Cmp(big.NewInt(0)) > 0:
					// Get to address
					toAddr := strings.ToLower(tx.To().Hex())

					// Check if address exists in network addresses list
					if userId, ok := addressList[toAddr]; ok {
						// Get sender address
						from, err := types.Sender(types.LatestSignerForChainID(chainID), tx)
						if err != nil {
							log.Printf("[%s] Failed to get sender for tx '%s': %v", bsc.NetworkCode, txHash, err)
							continue
						}
						fromAddr := strings.ToLower(from.Hex())

						// Skip, if transaction sent from master wallet
						if fromAddr == masterAddr {
							log.Printf("[%s] Skipping '%s' tx from master wallet", bsc.NetworkCode, txHash)
							continue
						}

						// Get converted amount (in BNB)
						amount := utils.RemoveDecimals(tx.Value(), int64(bnbDecimals))

						localTxList = append(localTxList, &InboundTransaction{
							UserId:   userId,
							Currency: "BNB",
							TxId:     txHash,
							From:     fromAddr,
							To:       toAddr,
							Amount:   amount,
						})
					}

				// If it's an BEP20 token transfer
				// Data structure: 4 bytes method ID + 32 bytes address + 32 bytes amount
				case tx.To() != nil && len(tx.Data()) >= 68:
					// Check if method is transfer (a9059cbb)
					methodId := fmt.Sprintf("%x", tx.Data()[0:4])
					if methodId == "a9059cbb" {
						// Get contract address
						contractAddr := strings.ToLower(tx.To().Hex())

						// Get token name by contract address
						token, ok := config.GetTokenByContractAddr(bsc.NetworkCode, contractAddr)
						if !ok {
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
								if err := limiter.Wait(cancelCtx); err != nil {
									return
								}

								// Get contract instance from ABI
								var out []interface{}
								contract := bind.NewBoundContract(
									common.HexToAddress(contractAddr), bsc.ABI, bsc.Client, bsc.Client, bsc.Client,
								)

								// Fetch decimals from contract
								err = contract.Call(&bind.CallOpts{}, &out, "decimals")
								if err != nil {
									log.Printf("[%s] Failed to get contract decimals for '%s': %v",
										bsc.NetworkCode, contractAddr, err)
									continue
								}
								decimalsValue = int64(out[0].(uint8))

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
							fromAddr := strings.ToLower(from.Hex())

							// Skip, if transaction sent from master wallet
							if fromAddr == masterAddr {
								log.Printf("[%s] Skipping '%s' tx from master wallet", bsc.NetworkCode, txHash)
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
								From:     fromAddr,
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
				bsc.NetworkCode, blockNum, lastContinuousBlock, firstErr)
			break
		}
	}

	// Save last processed block in Redis
	if lastContinuousBlock > lastBlockNumber {
		err = redis.GetClient().HSet(ctx, "last_blocks", bsc.NetworkCode, lastContinuousBlock)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save last block in Redis: %w", bsc.NetworkCode, err)
		}
	}

	log.Printf("[%s] Searching finished successfully: processed %d blocks, found %d user raw txs",
		bsc.NetworkCode, lastContinuousBlock-lastBlockNumber, len(txList))
	return txList, nil
}

/* GetUserWallet private methods group */
// @private getWallet Get HD-wallet by index
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// Returns: Derived address and private key
func (bsc *Bsc) getWallet(wType string, wId *uint32) (string, string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + bsc.NetworkCode)
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

	return bsc.deriveWallet(mnemonic, index)
}

// @private deriveWallet Derive HD-wallet by derivation index from master wallet mnemonic
// mnemonic: Master wallet mnemonic
// userId: Unique user id for derivation index
// Returns: Derived address and private key
func (bsc *Bsc) deriveWallet(mnemonic string, userId uint32) (string, string, error) {
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

	// BIP44 derivation path: m/44'/60'/1'/0/{index} (accountKey = 1)
	path := []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 60,
		hdkeychain.HardenedKeyStart + 1,
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
	bnbDecimals := config.TokenSettings[bsc.NetworkCode]["BNB"].Decimals
	// Return converted balance (in BNB)
	return utils.RemoveDecimals(rawBalance, int64(bnbDecimals)), nil
}

// @private getBep20Balance Get wallet BEP20 token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: BEP20 token wallet balance
func (bsc *Bsc) getBep20Balance(contractAddr, addr string) (float64, error) {
	// Get contract instance
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), bsc.ABI, bsc.Client, bsc.Client, bsc.Client)

	// Get raw balance (in decimal)
	var balanceOut []interface{}
	err := contract.Call(&bind.CallOpts{}, &balanceOut, "balanceOf", common.HexToAddress(addr))
	if err != nil {
		return 0, fmt.Errorf("failed to get contract balance: %w", err)
	}
	rawBalance := balanceOut[0].(*big.Int)

	// Get contract decimals
	var decimalsOut []interface{}
	err = contract.Call(&bind.CallOpts{}, &decimalsOut, "decimals")
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}
	decimalsValue := int64(decimalsOut[0].(uint8))

	// Return converted balance with removed decimals
	return utils.RemoveDecimals(rawBalance, decimalsValue), nil
}

/* GetTransactionCommission private methods group */
// @private calcNativeFee Calc fee for BNB transaction
// Returns: Fee in BNB
func (bsc *Bsc) calcNativeFee() (float64, error) {
	// Get BNB decimals from config
	bnbDecimals := config.TokenSettings[bsc.NetworkCode]["BNB"].Decimals

	// Gas for native transfers is always 21000 (EVM level constant)
	gasLimit := uint64(21000)

	// Get current gas price
	gasPrice, err := bsc.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get current gas price: %w", err)
	}

	// Get total fee in WEI
	totalFeeWei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	// Return converted total fee in BNB
	return utils.RemoveDecimals(totalFeeWei, int64(bnbDecimals)), nil
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
	contractAddr, ok := config.GetContractAddress(bsc.NetworkCode, currency)
	if !ok {
		return 0, fmt.Errorf("contract address not found in config: %s/%s", bsc.NetworkCode, currency)
	}

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), bsc.ABI, bsc.Client, bsc.Client, bsc.Client)

	// Get contract decimals
	err := contract.Call(&bind.CallOpts{}, &out, "decimals")
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}
	decimalsValue := int64(out[0].(uint8))
	// Convert amount to WEI with contract decimals
	amountWei := utils.ApplyDecimals(amount, decimalsValue)

	// Get of converted to address and contract address
	to := common.HexToAddress(toAddr)
	contractAddress := common.HexToAddress(contractAddr)
	// Set params for transfer contract call
	data, err := bsc.ABI.Pack("transfer", to, amountWei)

	// Try to estimate gas limit, fallback to 65000
	cfg, _ := config.GetNetworkConfig(bsc.NetworkCode)
	gasLimit, err := bsc.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  common.HexToAddress(fromAddr),
		To:    &contractAddress,
		Value: big.NewInt(0),
		Data:  data,
	})
	if err != nil {
		gasLimit = uint64(cfg.GasLimit)
	} else {
		gasLimit = gasLimit + uint64(float64(gasLimit)*cfg.FeeBuffer) // +10% buffer
	}

	// Get current gas price
	gasPrice, err := bsc.Client.SuggestGasPrice(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Get total fee in WEI
	totalFeeWei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	// Get BNB decimals from config
	bnbDecimals := config.TokenSettings[bsc.NetworkCode]["BNB"].Decimals

	// Return converted total fee in BNB
	return utils.RemoveDecimals(totalFeeWei, int64(bnbDecimals)), nil
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
	txFee, err := bsc.calcNativeFee()
	if err != nil {
		return "", fmt.Errorf("failed to get BNB tx fee: %w", err)
	}
	// Check BNB balance before sending
	if bnbBalance < amount+txFee {
		return "", fmt.Errorf("insufficient balance: have %f %s, need %f + %f (fee) %s",
			bnbBalance, "BNB", amount, txFee, "BNB")
	}

	// Get BNB decimals from config
	bnbDecimals := config.TokenSettings[bsc.NetworkCode]["BNB"].Decimals
	// Convert amount to WEI
	amountWei := utils.ApplyDecimals(amount, int64(bnbDecimals))

	// Get converted to address
	to := common.HexToAddress(toAddr)

	// Gas for native transfers is always 21000 (EVM level constant)
	gasLimit := uint64(21000)

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
	contractAddr, ok := config.GetContractAddress(bsc.NetworkCode, currency)
	if !ok {
		return "", fmt.Errorf("contract address not found in config: %s/%s", bsc.NetworkCode, currency)
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

	// Get contract instance
	var out []interface{}
	contract := bind.NewBoundContract(common.HexToAddress(contractAddr), bsc.ABI, bsc.Client, bsc.Client, bsc.Client)

	// Get contract decimals
	err = contract.Call(&bind.CallOpts{}, &out, "decimals")
	if err != nil {
		return "", fmt.Errorf("failed to get contract decimals: %w", err)
	}
	decimalsValue := int64(out[0].(uint8))
	// Convert amount to WEI with contract decimals
	amountWei := utils.ApplyDecimals(amount, decimalsValue)

	// Get of converted to address and contract address
	to := common.HexToAddress(toAddr)
	contractAddress := common.HexToAddress(contractAddr)
	// Set params for transfer contract call
	data, err := bsc.ABI.Pack("transfer", to, amountWei)

	// Try to estimate gas limit, fallback to 65000
	cfg, _ := config.GetNetworkConfig(bsc.NetworkCode)
	gasLimit, err := bsc.Client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  common.HexToAddress(fromAddr),
		To:    &contractAddress,
		Value: big.NewInt(0),
		Data:  data,
	})
	if err != nil {
		gasLimit = uint64(cfg.GasLimit)
	} else {
		gasLimit = gasLimit + uint64(float64(gasLimit)*cfg.FeeBuffer) // +10% buffer
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
