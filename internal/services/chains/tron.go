package chains

import (
	"context"
	"crypto/tls"
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

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/fbsobreira/gotron-sdk/pkg/address"
	"github.com/fbsobreira/gotron-sdk/pkg/client"
	"github.com/fbsobreira/gotron-sdk/pkg/client/transaction"
	"github.com/fbsobreira/gotron-sdk/pkg/common/decimals"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/api"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/core"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

type Tron struct {
	BaseChainService
	Client          *client.GrpcClient
	RateLimit       int
	decimalsCache   map[string]int64
	decimalsCacheMu sync.RWMutex
}

// Struct for QuickNode tron auth
type tronAuth struct {
	token string
}

func (ta *tronAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"x-token": ta.token,
	}, nil
}

func (ta *tronAuth) RequireTransportSecurity() bool {
	return false
}

// Global package vars
var (
	tronInstance *Tron
	tronOnce     sync.Once
)

// GetTron Get Tron chain instance
func GetTron() *Tron {
	tronOnce.Do(func() {
		tronInstance = newTron()
	})
	return tronInstance
}

// @private newTron Create new instance of Tron (private constructor)
func newTron() *Tron {
	// Load config
	cfg, _ := config.Load()

	// Create new RPC client
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
		grpc.WithPerRPCCredentials(&tronAuth{cfg.QuickNodeApiKey}),
	}
	rpcClient := client.NewGrpcClient(cfg.TronRpcUrl)
	if err := rpcClient.Start(opts...); err != nil {
		log.Fatalf("[Tron] Failed to connect to RPC API: %v", err)
	}

	// Get network settings
	networkCode := "tron"
	nativeToken := config.NetworkSettings[networkCode].NativeToken

	return &Tron{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: nativeToken,
		},
		Client:        rpcClient,
		RateLimit:     cfg.QuickNodeRps,
		decimalsCache: make(map[string]int64),
	}
}

// GetCode Get network code
// Returns: Network code
func (tron *Tron) GetCode() string {
	return tron.NetworkCode
}

// GetUserWalletAddress Get user HD-wallet by user id
// userId: Unique user id
// Returns: User HD-wallet address
func (tron *Tron) GetUserWalletAddress(userId uint32) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + tron.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}
	mnemonic, ok := masterData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return "", fmt.Errorf("mnemonic not found in Vault")
	}

	// Derive user address from master mnemonic
	addr, _, err := tron.deriveWallet(mnemonic, userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive address for user %d: %w", userId, err)
	}

	return addr, nil
}

// GetUserWallet Get user wallet info (address and token balances)
// userId: Unique user id
// Returns: Map of user wallet info (address and balances of all available tokens)
func (tron *Tron) GetUserWallet(userId uint32) (map[string]any, error) {
	// Get user wallet address
	userAddr, _, err := tron.getWallet("user", &userId)
	if err != nil {
		return nil, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	// Get TRC20 token balances
	balances := make(map[string]float64)
	for token, contractAddr := range config.ContractsAddresses[tron.NetworkCode] {
		tokenBalance, err := tron.getTrc20Balance(contractAddr, userAddr)
		if err != nil {
			continue
		}

		balances[token] = tokenBalance
	}

	// Get TRX native balance
	trxBalance, err := tron.getNativeBalance(userAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance for user %d: %w", userId, err)
	}
	balances[tron.NativeToken] = trxBalance

	return map[string]any{
		"address":  userAddr,
		"balances": balances,
	}, nil
}

// GetUserWalletBalance Get token balance amount for user wallet
// userId: User id
// currency: Token ticker
// Returns Balance amount for token
func (tron *Tron) GetUserWalletBalance(userId uint32, currency string) (float64, error) {
	// Get user wallet address
	userAddr, _, err := tron.getWallet("user", &userId)
	if err != nil {
		return 0, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	var balance float64
	if currency == "TRX" {
		balance, err = tron.getNativeBalance(userAddr)
	} else {
		contractAddr, ok := config.ContractsAddresses[tron.NetworkCode][currency]
		if !ok {
			return 0, fmt.Errorf("unsupported currency %q on network %s", currency, tron.NetworkCode)
		}

		balance, err = tron.getTrc20Balance(contractAddr, userAddr)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get balance for user %d: %w", userId, err)
	}

	return balance, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of master wallet info (address and balances of all available tokens)
func (tron *Tron) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + tron.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("master address not found in Vault")
	}

	// Get TRC20 token balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[tron.NetworkCode] {
		tokenBalance, err := tron.getTrc20Balance(contractAddr, masterAddr)
		if err != nil {
			continue
		}

		balances[token] = tokenBalance
	}

	// Get TRX native balance
	trxBalance, err := tron.getNativeBalance(masterAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance: %w", err)
	}
	balances[tron.NativeToken] = trxBalance

	return map[string]any{
		"address":  masterAddr,
		"balances": balances,
	}, nil
}

// GetMasterWalletBalance Get token balance amount for master wallet
// currency: Token ticker
// Returns Balance amount for token
func (tron *Tron) GetMasterWalletBalance(currency string) (float64, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + tron.NetworkCode)
	if err != nil {
		return 0, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return 0, fmt.Errorf("master address not found in Vault")
	}

	var balance float64
	if currency == "TRX" {
		balance, err = tron.getNativeBalance(masterAddr)
	} else {
		contractAddr, ok := config.ContractsAddresses[tron.NetworkCode][currency]
		if !ok {
			return 0, fmt.Errorf("unsupported currency %q on network %s", currency, tron.NetworkCode)
		}

		balance, err = tron.getTrc20Balance(contractAddr, masterAddr)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get %s balance for %s master wallet: %w", currency, tron.NetworkCode, err)
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
func (tron *Tron) GetTransactionCommission(oType, wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (map[string]float64, error) {
	// Get wallet address by type and id
	walletAddr, _, err := tron.getWallet(wType, wId)
	if err != nil {
		return nil, err
	}

	// Calc chain fee for currency (in TRX value)
	var chainFee float64
	switch currency {
	case "TRX":
		chainFee, err = tron.calcNativeFee(walletAddr, toAddr, amount)
	default:
		chainFee, err = tron.calcTrc20Fee(currency, walletAddr, toAddr, amount)
	}
	if err != nil {
		return nil, err
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Load config
		cfg, _ := config.Load()

		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetClient().GetWithdrawalFee(context.Background(), currency, tron.NetworkCode)
		// Get AML check cost from config (in USD value)
		amlFee = cfg.AmlCheckCost
	}

	return map[string]float64{
		"chain_fee": chainFee, // In TRX
		"bybit_fee": bybitFee, // In currency value (f.e. USDT)
		"aml_fee":   amlFee,   // in USD
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status (success/pending/failed)
func (tron *Tron) GetTransactionStatus(hash, fromAddr string) (string, error) {
	tx, err := tron.Client.GetTransactionByID(hash)
	if err != nil {
		return "", fmt.Errorf("failed to get transaction %s: %w", hash, err)
	}

	if tx.Ret == nil || len(tx.Ret) == 0 {
		return "pending", nil
	}

	status := tx.Ret[0].ContractRet.String()
	return strings.ToLower(status), nil
}

// SendTransaction Send transaction to the network
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// Returns: Transaction hash
func (tron *Tron) SendTransaction(wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (string, error) {
	// Get wallet address and private key by type and id
	walletAddr, walletKey, err := tron.getWallet(wType, wId)
	if err != nil {
		return "", err
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "TRX":
		hash, err = tron.sendNative(walletAddr, toAddr, amount, walletKey)
	default:
		hash, err = tron.sendTrc20(currency, walletAddr, toAddr, amount, walletKey)
	}

	if err != nil {
		// Send notifications to channels
		wIdPart := ""
		if wType == "user" {
			wIdPart = fmt.Sprintf(" [ID: %d]", wId)
		}
		errMsg := fmt.Sprintf("[SendTransaction] [%s] failed to send transaction (%f %s) from %s%s to %s: %v",
			tron.NetworkCode, amount, currency, wType, wIdPart, toAddr, err)
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
func (tron *Tron) GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error) {
	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(tron.RateLimit), 1)

	// Get master wallet for network
	masterData, err := vault.GetClient().GetSecret("master/" + tron.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get master data: %w", tron.NetworkCode, err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("[%s] Master address not found in Vault", tron.NetworkCode)
	}

	// Get current block
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	currentBlock, err := tron.Client.GetNowBlock()
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get current block: %w", tron.NetworkCode, err)
	}
	// Get current block number
	currentBlockNum := currentBlock.BlockHeader.RawData.Number

	// Get last block number from Redis (or use current block number, if not exists)
	lastBlock, err := redis.GetClient().HGet(ctx, "last_blocks", tron.NetworkCode)
	var lastBlockNumber int64
	if err != nil || lastBlock == "" {
		lastBlockNumber = currentBlockNum - 1
		log.Printf("[%s] First run, starting from block %d (current: %d)",
			tron.NetworkCode, lastBlockNumber, currentBlockNum)
	} else {
		lastBlockNumber, _ = strconv.ParseInt(lastBlock, 10, 64)
	}

	// Check if current block not equal or less, than last block
	if currentBlockNum <= lastBlockNumber {
		log.Printf("[%s] No new blocks to process", tron.NetworkCode)
		return []*InboundTransaction{}, nil
	}

	// Get all address for network from DB
	addressList, err := tron.WalletRepo.GetAddressListByNetwork(tron.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get wallet address list: %w", tron.NetworkCode, err)
	}

	// Get TRX decimals from config
	trxDecimals := config.TokenSettings[tron.NetworkCode]["TRX"].Decimals

	log.Printf("[%s] Processing %d blocks from %d to %d",
		tron.NetworkCode, currentBlockNum-lastBlockNumber, lastBlockNumber+1, currentBlockNum)

	var (
		// Wait group to wait for all tx blocks goroutines to complete
		wg sync.WaitGroup
		// Semaphore for goroutines limit
		sem = semaphore.NewWeighted(40)
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
				log.Printf("[%s] Rate limit error for block %d: %v", tron.NetworkCode, blockNum, err)
				return
			}
			block, err := tron.Client.GetBlockByNum(blockNum)
			if err != nil {
				log.Printf("[%s] Failed to get block %d: %v", tron.NetworkCode, blockNum, err)
				// Cancel all another goroutines
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			log.Printf("[%s] Processing block %d with %d raw txs",
				tron.NetworkCode, blockNum, len(block.Transactions))

			// Local goroutine tx list
			localTxList := make([]*InboundTransaction, 0)

			// Process transactions in block
			for _, tx := range block.Transactions {
				if tx.Transaction.RawData == nil || len(tx.Transaction.RawData.Contract) == 0 {
					continue
				}

				// Get tx hash
				txHash := fmt.Sprintf("%x", tx.GetTxid())
				// Skip, if transaction is not success
				if tx.Result == nil || tx.Result.Code != api.Return_SUCCESS {
					continue
				}

				// Get contract entity and tx id
				contract := tx.Transaction.RawData.Contract[0]

				switch contract.Type {
				// If it's TRX token transaction
				case core.Transaction_Contract_TransferContract:
					// Get transfer contract entity
					transferContract := &core.TransferContract{}
					err := proto.Unmarshal(contract.Parameter.Value, transferContract)
					if err != nil {
						log.Printf("[%s] Failed to unmarshal transfer contract: %v", tron.NetworkCode, err)
						continue
					}

					if transferContract.OwnerAddress == nil || transferContract.ToAddress == nil {
						log.Printf("[%s] Error getting transfer contract owner and to address: %v", tron.NetworkCode, err)
						continue
					}

					// Get from and to address
					fromAddr := address.Address(transferContract.OwnerAddress).String()
					toAddr := address.Address(transferContract.ToAddress).String()

					// Check if address exists in network addresses list
					if userId, ok := addressList[toAddr]; ok {
						// Skip, if transaction sent from master wallet
						if fromAddr == masterAddr {
							log.Printf("[%s] Skipping '%s' tx from master wallet", tron.NetworkCode, txHash)
							continue
						}

						// Get converted amount (in TRX)
						amount, _ := decimals.RemoveDecimals(big.NewInt(transferContract.Amount), int64(trxDecimals)).Float64()

						localTxList = append(localTxList, &InboundTransaction{
							UserId:   userId,
							Currency: "TRX",
							TxId:     txHash,
							From:     fromAddr,
							To:       toAddr,
							Amount:   amount,
						})
					}

				// If it's TRC20 token transaction
				case core.Transaction_Contract_TriggerSmartContract:
					// Get trigger contract entity
					triggerContract := &core.TriggerSmartContract{}
					err := proto.Unmarshal(contract.Parameter.Value, triggerContract)
					if err != nil {
						log.Printf("[%s] Failed to unmarshal trigger smart contract: %v", tron.NetworkCode, err)
						continue
					}

					// Get contract address
					contractAddr := address.Address(triggerContract.ContractAddress).String()

					// Get token name by contract address
					token, ok := config.GetTokenByContractAddr(tron.NetworkCode, contractAddr)
					if !ok {
						continue
					}

					if triggerContract.OwnerAddress == nil {
						log.Printf("[%s] Trigger smart contract owner address is nil in block %d",
							tron.NetworkCode, blockNum)
						continue
					}

					// Check if it's an TRC20 token transfer
					// Data structure: 4 bytes method ID + 32 bytes address + 32 bytes amount
					if len(triggerContract.Data) == 68 {
						// Check if method is transfer (a9059cbb)
						methodId := fmt.Sprintf("%x", triggerContract.Data[0:4])
						if methodId == "a9059cbb" {
							// Extract to address (bytes 4-36, take last 21 bytes)
							toAddrBytes := triggerContract.Data[15:36]
							// Extract amount (bytes 36-68)
							rawAmount := new(big.Int).SetBytes(triggerContract.Data[36:68])

							// Get from and to address
							fromAddr := address.Address(triggerContract.OwnerAddress).String()
							toAddr := address.Address(toAddrBytes).String()

							// Check if address exists in network addresses list
							if userId, ok := addressList[toAddr]; ok {
								// Skip, if transaction sent from master wallet
								if fromAddr == masterAddr {
									log.Printf("[%s] Skipping '%s' tx from master wallet", tron.NetworkCode, txHash)
									continue
								}

								// Trying to get contract decimals from cache
								tron.decimalsCacheMu.RLock()
								decimalsValue, exists := tron.decimalsCache[contractAddr]
								tron.decimalsCacheMu.RUnlock()

								// Fetch decimals from contract
								if !exists {
									if err := limiter.Wait(cancelCtx); err != nil {
										return
									}

									decimalsResult, err := tron.Client.TRC20GetDecimals(contractAddr)
									if err != nil {
										log.Printf("[%s] Failed to get contract decimals for '%s': %v",
											tron.NetworkCode, contractAddr, err)
										continue
									}
									decimalsValue = decimalsResult.Int64()

									// Save decimals to cache
									tron.decimalsCacheMu.Lock()
									tron.decimalsCache[contractAddr] = decimalsValue
									tron.decimalsCacheMu.Unlock()
								}

								// Get converted amount
								amount, _ := decimals.RemoveDecimals(rawAmount, decimalsValue).Float64()

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
				tron.NetworkCode, blockNum, lastContinuousBlock, firstErr)
			break
		}
	}

	// Save last processed block in Redis
	if lastContinuousBlock > lastBlockNumber {
		err = redis.GetClient().HSet(ctx, "last_blocks", tron.NetworkCode, lastContinuousBlock)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save last block in Redis: %w", tron.NetworkCode, err)
		}
	}

	log.Printf("[%s] Searching finished successfully: processed %d blocks, found %d user raw txs",
		tron.NetworkCode, lastContinuousBlock-lastBlockNumber, len(txList))
	return txList, nil
}

/* GetUserWallet private methods group */
// @private getWallet Get HD-wallet by index
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// Returns: Derived address and private key
func (tron *Tron) getWallet(wType string, wId *uint32) (string, string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + tron.NetworkCode)
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

	return tron.deriveWallet(mnemonic, index)
}

// @private deriveWallet Derive HD-wallet by derivation index from master wallet mnemonic
// mnemonic: Master wallet mnemonic
// userId: Unique user id for derivation index
// Returns: Derived address and private key
func (tron *Tron) deriveWallet(mnemonic string, userId uint32) (string, string, error) {
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

	// BIP44 derivation path: m/44'/195'/0'/0/{index} (accountKey = 0)
	path := []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 195,
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
	privateKey, err := key.ECPrivKey()
	if err != nil {
		return "", "", fmt.Errorf("failed to get private key: %w", err)
	}
	ecdsaPrivKey := privateKey.ToECDSA()

	// Get address
	addr := address.PubkeyToAddress(ecdsaPrivKey.PublicKey).String()

	return addr, hex.EncodeToString(privateKey.Serialize()), nil
}

/* GetMasterWallet private methods group */
// @private getDecimals Get token decimals by contract address with caching
// contractAddr: Token contract address
// Returns: Token decimals
func (tron *Tron) getDecimals(contractAddr string) (int64, error) {
	tron.decimalsCacheMu.RLock()
	decimalsValue, exists := tron.decimalsCache[contractAddr]
	tron.decimalsCacheMu.RUnlock()

	if exists {
		return decimalsValue, nil
	}

	result, err := tron.Client.TRC20GetDecimals(contractAddr)
	if err != nil {
		return 0, err
	}
	decimalsValue = result.Int64()

	tron.decimalsCacheMu.Lock()
	tron.decimalsCache[contractAddr] = decimalsValue
	tron.decimalsCacheMu.Unlock()

	return decimalsValue, nil
}

// @private getNativeBalance Get wallet TRX balance
// addr: Wallet address
// Returns: TRX wallet balance
func (tron *Tron) getNativeBalance(addr string) (float64, error) {
	// Get tron address instance
	tronAddr, err := address.Base58ToAddress(addr)
	if err != nil {
		return 0, err
	}

	// Get account instance by wallet address
	account, err := tron.Client.GetAccount(tronAddr.String())
	if err != nil {
		// Account not found — not activated, balance is 0
		if strings.Contains(err.Error(), "account not found") {
			return 0, nil
		}
		return 0, err
	}
	// Get raw balance (in SUN)
	rawBalance := big.NewInt(account.Balance)

	// Get TRX decimals
	trxDecimals := config.TokenSettings[tron.NetworkCode]["TRX"].Decimals
	// Get converted balance (in TRX)
	trxBalance, _ := decimals.RemoveDecimals(rawBalance, int64(trxDecimals)).Float64()

	return trxBalance, nil
}

// @private getTrc20Balance Get wallet TRC20 token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: TRC20 token wallet balance
func (tron *Tron) getTrc20Balance(contractAddr, addr string) (float64, error) {
	// Get raw contract balance
	rawBalance, err := tron.Client.TRC20ContractBalance(addr, contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract balance: %w", err)
	}

	// Get contract decimals
	decimalsValue, err := tron.getDecimals(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}

	// Get converted balance
	convertedBalance, _ := decimals.RemoveDecimals(rawBalance, decimalsValue).Float64()
	return convertedBalance, nil
}

/* GetTransactionCommission private methods group */
// @private calcNativeFee Calc fee for TRX transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in TRX
func (tron *Tron) calcNativeFee(fromAddr, toAddr string, amount float64) (float64, error) {
	// Get current chain params
	chainParams, err := tron.getCurrentChainParams()
	if err != nil {
		return 0, fmt.Errorf("failed to get current chain params: %w", err)
	}

	// Get TRX decimals
	trxDecimals := config.TokenSettings[tron.NetworkCode]["TRX"].Decimals
	// Convert amount to SUN
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), int64(trxDecimals))

	// Check, if to account activated
	activationFee := big.NewInt(0)
	toAccount, err := tron.Client.GetAccount(toAddr)
	// If not activated, set 1 TRX of activation fee (in SUN)
	if err != nil || toAccount.Address == nil {
		activationFee, _ = decimals.ApplyDecimals(big.NewFloat(1), int64(trxDecimals)) // 1 TRX in SUN
	}

	// Create tx to measure raw data size (don't send it)
	tx, err := tron.Client.Transfer(fromAddr, toAddr, amountSun.Int64())
	if err != nil {
		return 0, fmt.Errorf("failed to create transfer transaction: %v", err)
	}

	// Get serialized protobuf tx raw_data
	rawDataBytes, err := proto.Marshal(tx.GetTransaction().GetRawData())
	if err != nil {
		return 0, fmt.Errorf("failed to marshal TRX transaction raw data: %v", err)
	}
	// Get needed bandwidth - length of serialized data (~268 bytes for TRX)
	bandwidthNeeded := int64(len(rawDataBytes))

	// Calc bandwidth cost
	bandwidthCost := int64(0)
	// Get from address account resources info
	resources, err := tron.Client.GetAccountResource(fromAddr)
	if err == nil {
		// Calc available account bandwidth
		availableBandwidth := (resources.NetLimit - resources.NetUsed) +
			(resources.FreeNetLimit - resources.FreeNetUsed)

		if availableBandwidth < bandwidthNeeded {
			// Not enough free bandwidth
			paidBandwidth := bandwidthNeeded - availableBandwidth
			if paidBandwidth > 0 {
				bandwidthCost = paidBandwidth * chainParams["bandwidthPrice"]
			}
		}
	} else {
		// If we cannot get resources, need to pay for the entire bandwidth
		bandwidthCost = bandwidthNeeded * chainParams["bandwidthPrice"]
	}

	// Get total fee in SUN
	totalFeeSun := bandwidthCost + activationFee.Int64()
	// Convert total fee to TRX
	totalFee, _ := decimals.RemoveDecimals(big.NewInt(totalFeeSun), int64(trxDecimals)).Float64()

	return totalFee, nil
}

// @private calcTrc20Fee Calc fee for TRC20 token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in TRX
func (tron *Tron) calcTrc20Fee(currency, fromAddr, toAddr string, amount float64) (float64, error) {
	currency = strings.ToUpper(currency)

	// Get current chain params
	chainParams, err := tron.getCurrentChainParams()
	if err != nil {
		return 0, fmt.Errorf("failed to get current chain params: %w", err)
	}

	// Get token contract address
	contractAddr, ok := config.GetContractAddress(tron.NetworkCode, currency)
	if !ok {
		return 0, fmt.Errorf("contract address not found in config: %s/%s", tron.NetworkCode, currency)
	}

	// Get contract decimals
	contractDecimals, err := tron.getDecimals(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals for %s: %w", currency, err)
	}
	// Convert amount to SUN with contract decimals
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), contractDecimals)

	// Try to calc energy needed value
	var energyNeeded int
	energyEstimate, err := tron.Client.EstimateEnergy(
		fromAddr, contractAddr,
		"transfer(address,uint256)",
		fmt.Sprintf(`[{"address":"%s"},{"uint256":"%s"}]`, toAddr, amountSun.String()),
		0, "", 0,
	)
	if err == nil && energyEstimate.EnergyRequired > 0 {
		energyNeeded = int(energyEstimate.EnergyRequired)
	} else {
		// Fallback from config
		energyNeeded = config.TokenSettings[tron.NetworkCode][currency].EstimatedEnergy
	}

	// Check, if to address account has USDT (affects cost)
	trc20Balance, _ := tron.getTrc20Balance(contractAddr, toAddr)
	recipientHasBalance := trc20Balance > 0
	if !recipientHasBalance {
		// If to address account doesn't have USDT, need x2 more energy
		energyNeeded = energyNeeded * 2
	}

	// Create TRC20 tx entity
	tx, err := tron.Client.TRC20Send(fromAddr, toAddr, contractAddr, amountSun, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to trigger contract: %w", err)
	}

	// Get serialized protobuf tx raw_data
	rawDataBytes, err := proto.Marshal(tx.GetTransaction().GetRawData())
	if err != nil {
		return 0, fmt.Errorf("failed to marshal TRC20 transaction raw data: %v", err)
	}
	// Get needed bandwidth - length of serialized data (~345 bytes for TRC20)
	bandwidthNeeded := len(rawDataBytes)

	// Calc energy and bandwidth cost
	energyCost := 0
	bandwidthCost := 0
	// Get from address account resources info
	resources, err := tron.Client.GetAccountResource(fromAddr)
	if err == nil {
		// Calc available account energy
		availableEnergy := int(resources.EnergyLimit - resources.EnergyUsed)
		if availableEnergy < energyNeeded {
			paidEnergy := energyNeeded - availableEnergy
			if paidEnergy < 0 {
				paidEnergy = 0
			}
			energyCost = paidEnergy * int(chainParams["energyPrice"])
		}

		// Calc available account bandwidth
		availableBandwidth := int((resources.NetLimit - resources.NetUsed) +
			(resources.FreeNetLimit - resources.FreeNetUsed))
		if availableBandwidth < bandwidthNeeded {
			paidBandwidth := bandwidthNeeded - availableBandwidth
			if paidBandwidth < 0 {
				paidBandwidth = 0
			}
			bandwidthCost = paidBandwidth * int(chainParams["bandwidthPrice"])
		}
	} else {
		// If we cannot get resources, need to pay for all
		energyCost = energyNeeded * int(chainParams["energyPrice"])
		bandwidthCost = bandwidthNeeded * int(chainParams["bandwidthPrice"])
	}

	// Get total fee in SUN
	totalFeeUnits := energyCost + bandwidthCost
	// Get TRX decimals
	trxDecimals := config.TokenSettings[tron.NetworkCode]["TRX"].Decimals
	// Convert total fee to TRX
	totalFee, _ := decimals.RemoveDecimals(big.NewInt(int64(totalFeeUnits)), int64(trxDecimals)).Float64()

	return totalFee, nil
}

// @private getCurrentChainParams Get current chain params (energyPrice and bandwidthPrice)
// Returns: Map of current chain params (getEnergyFee and getTransactionFee)
func (tron *Tron) getCurrentChainParams() (map[string]int64, error) {
	// Get current chain params
	params, err := tron.Client.Client.GetChainParameters(context.Background(), &api.EmptyMessage{})
	if err != nil {
		return nil, fmt.Errorf("failed to get chain parameters: %w", err)
	}

	// Get only energyPrice and bandwidthPrice from params structure
	chainParams := make(map[string]int64)
	for _, param := range params.ChainParameter {
		switch param.Key {
		case "getEnergyFee":
			chainParams["energyPrice"] = param.Value
		case "getTransactionFee":
			chainParams["bandwidthPrice"] = param.Value
		}
	}

	return chainParams, nil
}

/* SendTransaction private methods group */
// @private sendNative Send native TRX transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value (in TRX)
// privateKey: Wallet private key
// Returns: Transaction hash
func (tron *Tron) sendNative(fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	// Get TRX balance
	trxBalance, err := tron.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get TRX balance: %w", err)
	}
	// Get tx fee (in TRX)
	txFee, err := tron.calcNativeFee(fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get TRX tx fee: %w", err)
	}
	// Check TRX balance before sending
	if trxBalance < amount+txFee {
		return "", fmt.Errorf("insufficient balance: have %f %s, need %f + %f (fee) %s",
			trxBalance, "TRX", amount, txFee, "TRX")
	}

	// Get TRX decimals
	trxDecimals := config.TokenSettings[tron.NetworkCode]["TRX"].Decimals
	// Convert amount to SUN including decimals
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), int64(trxDecimals))

	// Create TRX transaction
	tx, err := tron.Client.Transfer(fromAddr, toAddr, amountSun.Int64())
	if err != nil {
		return "", fmt.Errorf("failed to create TRX transaction: %v", err)
	}

	// Sign and send transaction to the network
	return tron.signAndSend(tx, privateKey)
}

// @private sendTrc20 Send TRC20 token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value (in currency value)
// privateKey: Wallet private key
// Returns: Transaction hash
func (tron *Tron) sendTrc20(currency, fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	currency = strings.ToUpper(currency)

	// Get token contract address
	contractAddr, ok := config.GetContractAddress(tron.NetworkCode, currency)
	if !ok {
		return "", fmt.Errorf("contract address not found in config: %s/%s", tron.NetworkCode, currency)
	}

	// Get TRX and token balances
	trxBalance, err := tron.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get TRX balance: %w", err)
	}
	trc20Balance, err := tron.getTrc20Balance(contractAddr, fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get %s balance: %w", currency, err)
	}
	// Get tx fee (in TRX)
	txFee, err := tron.calcTrc20Fee(currency, fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get %s tx fee: %w", currency, err)
	}
	// Check TRX and currency balances before sending
	if trxBalance < txFee {
		return "", fmt.Errorf("insufficient TRX balance: have %f %s, need %f %s", trxBalance, "TRX", txFee, "TRX")
	}
	if trc20Balance < amount {
		return "", fmt.Errorf("insufficient currency balance: have %f %s, need %f %s", trc20Balance, currency, amount, currency)
	}

	// Get contract decimals
	contractDecimals, err := tron.getDecimals(contractAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get contract decimals: %w", err)
	}
	// Convert amount to SUN with contract decimals
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), contractDecimals)

	// Create TRC20 transaction
	tx, err := tron.Client.TRC20Send(fromAddr, toAddr, contractAddr, amountSun, 0)
	if err != nil {
		return "", fmt.Errorf("failed to create USDT transaction: %v", err)
	}

	// Sign and send transaction to the network
	return tron.signAndSend(tx, privateKey)
}

// @private signAndSend Sign and send transaction to the network
// tx: Transaction struct
// privateKey: Wallet private key
// Returns: Transaction hash
func (tron *Tron) signAndSend(tx *api.TransactionExtention, privateKey string) (string, error) {
	// Convert hex private key string to *ecdsa.PrivateKey
	ecdsaPk, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s wallet private key: %w", tron.NetworkCode, err)
	}

	// Sign transaction
	signedTx, err := transaction.SignTransactionECDSA(tx.Transaction, ecdsaPk)
	if err != nil {
		return "", fmt.Errorf("failed to sign %s transaction: %w", tron.NetworkCode, err)
	}

	// Broadcast transaction
	result, err := tron.Client.Broadcast(signedTx)
	if err != nil {
		return "", fmt.Errorf("failed to broadcast %s transaction: %w", tron.NetworkCode, err)
	}

	// Check result and return transaction hash
	if !result.Result || result.Code != api.Return_SUCCESS {
		return "", fmt.Errorf("%s transaction failed: [%d] %s", tron.NetworkCode, result.Code, result.Message)
	}
	return hex.EncodeToString(tx.Txid), nil
}
