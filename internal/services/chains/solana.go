package chains

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
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
	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	ata "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
)

type Solana struct {
	BaseChainService
	Client          *rpc.Client
	RateLimit       int
	decimalsCache   map[string]int64
	decimalsCacheMu sync.RWMutex
}

// Global package vars
var (
	solanaInstance *Solana
	solanaOnce     sync.Once
)

// GetSolana Get Solana chain instance
func GetSolana() *Solana {
	solanaOnce.Do(func() {
		solanaInstance = newSolana()
	})
	return solanaInstance
}

// @private newSolana Create new instance of Solana (private constructor)
func newSolana() *Solana {
	// Load config
	cfg, _ := config.Load()

	// Create new RPC client
	client := rpc.New(cfg.SolRpcUrl + "/" + cfg.QuickNodeApiKey)

	// Get network settings
	networkCode := "solana"
	nativeToken := config.NetworkSettings[networkCode].NativeToken

	return &Solana{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: nativeToken,
		},
		Client:        client,
		RateLimit:     cfg.QuickNodeRps,
		decimalsCache: make(map[string]int64),
	}
}

// GetCode Get network code
func (sol *Solana) GetCode() string {
	return sol.NetworkCode
}

// GetUserWalletAddress Get user HD-wallet by user id
// userId: Unique user id
// Returns: User HD-wallet address
func (sol *Solana) GetUserWalletAddress(userId uint32) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + sol.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}
	mnemonic, ok := masterData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return "", fmt.Errorf("mnemonic not found in Vault")
	}

	addr, _, err := sol.deriveWallet(mnemonic, userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive address for user %d: %w", userId, err)
	}

	return addr, nil
}

// GetUserWallet Get user wallet info (address and token balances)
// userId: Unique user id
// Returns: Map of user wallet info (address and balances of all available tokens)
func (sol *Solana) GetUserWallet(userId uint32) (map[string]any, error) {
	// Get user wallet address
	userAddr, _, err := sol.getWallet("user", &userId)
	if err != nil {
		return nil, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	// Get SPL token balances
	balances := make(map[string]float64)
	for tokenName, contractAddr := range config.ContractsAddresses[sol.NetworkCode] {
		tokenBalance, err := sol.getSplBalance(contractAddr, userAddr)
		if err != nil {
			continue
		}

		balances[tokenName] = tokenBalance
	}

	// Get SOL native balance
	solBalance, err := sol.getNativeBalance(userAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance: %w", err)
	}
	balances[sol.NativeToken] = solBalance

	return map[string]any{
		"address":  userAddr,
		"balances": balances,
	}, nil
}

// GetUserWalletBalance Get token balance amount for user wallet
// userId: User id
// currency: Token ticker
// Returns Balance amount for token
func (sol *Solana) GetUserWalletBalance(userId uint32, currency string) (float64, error) {
	// Get user wallet address
	userAddr, _, err := sol.getWallet("user", &userId)
	if err != nil {
		return 0, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	var balance float64
	if currency == "SOL" {
		balance, err = sol.getNativeBalance(userAddr)
	} else {
		contractAddr, ok := config.ContractsAddresses[sol.NetworkCode][currency]
		if !ok {
			return 0, fmt.Errorf("unsupported currency %q on network %s", currency, sol.NetworkCode)
		}

		balance, err = sol.getSplBalance(contractAddr, userAddr)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get balance for user %d: %w", userId, err)
	}

	return balance, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of master wallet info (address and balances of all available tokens)
func (sol *Solana) GetMasterWallet() (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + sol.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("master address not found in Vault")
	}

	// Get SPL token balances
	balances := make(map[string]any)
	for tokenName, contractAddr := range config.ContractsAddresses[sol.NetworkCode] {
		tokenBalance, err := sol.getSplBalance(contractAddr, masterAddr)
		if err != nil {
			continue
		}

		balances[tokenName] = tokenBalance
	}

	// Get SOL native balance
	solBalance, err := sol.getNativeBalance(masterAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance: %w", err)
	}
	balances[sol.NativeToken] = solBalance

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
// memo: Memo tag
// Returns: Map of all fee types (chain_fee, bybit_fee, aml_fee)
func (sol *Solana) GetTransactionCommission(oType, wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (map[string]float64, error) {
	// Get wallet address by type and id
	walletAddr, _, err := sol.getWallet(wType, wId)
	if err != nil {
		return nil, err
	}

	// Calc chain fee for currency (in SOL value)
	var chainFee float64
	switch currency {
	case "SOL":
		chainFee, err = sol.calcNativeFee(walletAddr, toAddr, amount)
	default:
		chainFee, err = sol.calcSplFee(currency, walletAddr, toAddr, amount)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction commission: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Load config
		cfg, _ := config.Load()

		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetClient().GetWithdrawalFee(context.Background(), currency, sol.NetworkCode)
		// Get AML check cost from config (in USD value)
		amlFee = cfg.AmlCheckCost
	}

	return map[string]float64{
		"chain_fee": chainFee, // In SOL
		"bybit_fee": bybitFee, // In currency value
		"aml_fee":   amlFee,   // in USD
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status (success/pending/failed)
func (sol *Solana) GetTransactionStatus(hash, toAddr string) (string, error) {
	ctx := context.Background()

	// Parse signature from hash
	signature, err := solana.SignatureFromBase58(hash)
	if err != nil {
		return "", fmt.Errorf("invalid transaction hash: %w", err)
	}

	// Get signature statuses
	statuses, err := sol.Client.GetSignatureStatuses(ctx, true, signature)
	if err != nil {
		return "", fmt.Errorf("failed to get signature statuses: %w", err)
	}

	// Check if we got a result
	if statuses == nil || len(statuses.Value) == 0 || statuses.Value[0] == nil {
		return "pending", nil
	}

	// Get status
	status := statuses.Value[0]

	// Check for errors
	if status.Err != nil {
		return "failed", nil
	}

	// Check confirmation status
	if status.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
		return "success", nil
	} else if status.ConfirmationStatus == rpc.ConfirmationStatusConfirmed {
		return "success", nil
	} else if status.ConfirmationStatus == rpc.ConfirmationStatusProcessed {
		return "pending", nil
	}

	return "pending", nil
}

// SendTransaction Send transaction to the network
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (sol *Solana) SendTransaction(wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (string, error) {
	// Get wallet address and private key by type and id
	walletAddr, walletKey, err := sol.getWallet(wType, wId)
	if err != nil {
		return "", err
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "SOL":
		hash, err = sol.sendNative(walletAddr, toAddr, amount, walletKey)
	default:
		hash, err = sol.sendSpl(currency, walletAddr, toAddr, amount, walletKey)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, nil
}

// GetInboundTransactions Get new inbound transactions list
// Returns: Inbound transactions list
func (sol *Solana) GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error) {
	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(sol.RateLimit), 1)

	// Get current block (slot) number
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	currentBlockNum, err := sol.Client.GetSlot(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get current slot: %w", sol.NetworkCode, err)
	}

	// Get last block number from Redis (or use current block number, if not exists)
	lastBlock, err := redis.GetClient().HGet(ctx, "last_blocks", sol.NetworkCode)
	var lastBlockNumber uint64
	if err != nil || lastBlock == "" {
		lastBlockNumber = currentBlockNum - 1
		log.Printf("[%s] First run, starting from block %d (current: %d)",
			sol.NetworkCode, lastBlockNumber, currentBlockNum)
	} else {
		lastBlockNumber, _ = strconv.ParseUint(lastBlock, 10, 64)
	}

	// Check if current block not equal or less, than last block
	if currentBlockNum <= lastBlockNumber {
		log.Printf("[%s] No new blocks to process", sol.NetworkCode)
		return []*InboundTransaction{}, nil
	}

	// Get all addresses for network from DB
	addressList, err := sol.WalletRepo.GetAddressListByNetwork(sol.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get wallet address list: %w", sol.NetworkCode, err)
	}
	// Convert to map[PublicKey]userId for fast search
	addressMap := make(map[solana.PublicKey]uint, len(addressList))
	for addr, userId := range addressList {
		pubKey, err := solana.PublicKeyFromBase58(addr)
		if err != nil {
			continue
		}
		addressMap[pubKey] = userId
	}

	// Get SOL decimals from config
	solDecimals := config.TokenSettings[sol.NetworkCode]["SOL"].Decimals

	// Get all new blocks list
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	blocks, err := sol.Client.GetBlocks(ctx, lastBlockNumber+1, &currentBlockNum, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("[%s] failed to get blocks: %w", sol.NetworkCode, err)
	}
	if len(blocks) == 0 {
		return []*InboundTransaction{}, nil
	}

	log.Printf("[%s] Processing transactions for %d user addresses from block %d to %d",
		sol.NetworkCode, len(addressList), lastBlockNumber+1, currentBlockNum)

	var (
		// Flag for getting versioned transactions
		version = uint64(0)
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
	for _, blockNum := range blocks {
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

			// Get block entity
			if err := limiter.Wait(cancelCtx); err != nil {
				log.Printf("[%s] Rate limit error for block %d: %v", sol.NetworkCode, blockNum, err)
				return
			}
			block, err := sol.Client.GetBlockWithOpts(cancelCtx, blockNum, &rpc.GetBlockOpts{
				Encoding:                       solana.EncodingBase64,
				Commitment:                     rpc.CommitmentFinalized,
				TransactionDetails:             rpc.TransactionDetailsFull,
				MaxSupportedTransactionVersion: &version,
			})
			if err != nil {
				log.Printf("[%s] Failed to get block %d: %v", sol.NetworkCode, blockNum, err)
				// Cancel all another goroutines
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			log.Printf("[%s] Processing block %d with %d raw transactions",
				sol.NetworkCode, blockNum, len(block.Transactions))

			// Local goroutine tx list
			localTxList := make([]*InboundTransaction, 0)

			// Process transactions in block
			for _, tx := range block.Transactions {
				// Skip, if transaction failed
				if tx.Meta != nil && tx.Meta.Err != nil {
					continue
				}

				// Get parsed transaction entity
				parsedTx, err := tx.GetTransaction()
				if err != nil {
					log.Printf("[%s] Failed to get transaction in block %d: %v", sol.NetworkCode, blockNum, err)
					continue
				}
				// Get transaction message
				msg := parsedTx.Message

				// Check, if transaction has any of user addresses (by public key)
				hasOurAddress := false
				for _, key := range msg.AccountKeys {
					if _, ok := addressMap[key]; ok {
						hasOurAddress = true
						break
					}
				}
				if !hasOurAddress {
					continue
				}

				// Get transaction hash
				txHash := parsedTx.Signatures[0].String()

				// Process all instructions in transaction
				for _, instruction := range msg.Instructions {
					if int(instruction.ProgramIDIndex) >= len(msg.AccountKeys) {
						continue
					}
					// Get program id
					programId := msg.AccountKeys[instruction.ProgramIDIndex]

					switch true {
					// If it's native SOL transfer
					case programId.Equals(solana.SystemProgramID):
						// Check data format
						if len(instruction.Data) < 12 {
							continue
						}
						if binary.LittleEndian.Uint32(instruction.Data[0:4]) != 2 {
							continue
						}
						if len(instruction.Accounts) < 2 {
							continue
						}

						// Extract accounts data
						toIdx := instruction.Accounts[1]
						if int(toIdx) >= len(msg.AccountKeys) {
							continue
						}

						// Get user id from address map (by public key)
						toPubKey := msg.AccountKeys[toIdx]
						userId, ok := addressMap[toPubKey]
						if !ok {
							continue
						}

						// Get from address
						fromIdx := instruction.Accounts[0]
						if int(fromIdx) >= len(msg.AccountKeys) {
							continue
						}
						fromAddr := msg.AccountKeys[fromIdx].String()
						// Get converted amount (in SOL)
						rawAmount := binary.LittleEndian.Uint64(instruction.Data[4:12])
						amount := utils.RemoveDecimals(big.NewInt(int64(rawAmount)), int64(solDecimals))

						localTxList = append(localTxList, &InboundTransaction{
							UserId:   userId,
							Currency: "SOL",
							TxId:     txHash,
							From:     fromAddr,
							To:       toPubKey.String(),
							Amount:   amount,
						})

					// If it's SPL token transfer
					case programId.Equals(solana.TokenProgramID):
						// Check instruction type
						if len(instruction.Data) < 9 {
							continue
						}

						instructionType := instruction.Data[0]
						if instructionType != 3 && instructionType != 12 {
							continue
						}
						if len(instruction.Accounts) < 2 {
							continue
						}

						// Get receiver key
						destIdx := instruction.Accounts[1]
						if int(destIdx) >= len(msg.AccountKeys) {
							continue
						}
						destTokenAccountKey := msg.AccountKeys[destIdx]

						// Trying to search receiver (owner) in PostTokenBalances
						var ownerPubKey solana.PublicKey
						var mintAddr string
						var found bool
						if tx.Meta != nil {
							for _, balance := range tx.Meta.PostTokenBalances {
								if balance.AccountIndex == destIdx && balance.Owner != nil {
									ownerPubKey = *balance.Owner
									mintAddr = balance.Mint.String()
									found = true
									break
								}
							}
						}
						// Fallback to RPC, if receiver not found in metadata
						if !found {
							if err := limiter.Wait(cancelCtx); err != nil {
								return
							}

							// Get account info
							destAccountInfo, err := sol.Client.GetAccountInfo(cancelCtx, destTokenAccountKey)
							if err != nil || destAccountInfo == nil || destAccountInfo.Value == nil {
								continue
							}

							// Parse token account data
							var destTokenData token.Account
							if err := bin.NewBinDecoder(destAccountInfo.Value.Data.GetBinary()).Decode(&destTokenData); err != nil {
								continue
							}

							ownerPubKey = destTokenData.Owner
							mintAddr = destTokenData.Mint.String()
						}

						// Get user id from address map (by receiver public key)
						userId, ok := addressMap[ownerPubKey]
						if !ok {
							continue
						}

						// Get token symbol from config
						tokenSymbol, ok := config.GetTokenByContractAddr(sol.NetworkCode, mintAddr)
						if !ok {
							continue
						}

						// Trying to get contract decimals from cache
						sol.decimalsCacheMu.RLock()
						decimalsValue, exists := sol.decimalsCache[mintAddr]
						sol.decimalsCacheMu.RUnlock()
						// Fetch data from contract
						if !exists {
							if err := limiter.Wait(cancelCtx); err != nil {
								return
							}

							// Get contract data
							mintPubKey, _ := solana.PublicKeyFromBase58(mintAddr)
							mintAccount, err := sol.Client.GetAccountInfo(cancelCtx, mintPubKey)
							if err != nil || mintAccount == nil || mintAccount.Value == nil {
								continue
							}

							// Parse contract data
							var mintData token.Mint
							if err := bin.NewBinDecoder(mintAccount.Value.Data.GetBinary()).Decode(&mintData); err != nil {
								continue
							}
							decimalsValue = int64(mintData.Decimals)

							// Save decimals to cache
							sol.decimalsCacheMu.Lock()
							sol.decimalsCache[mintAddr] = decimalsValue
							sol.decimalsCacheMu.Unlock()
						}

						// Get from address
						fromAddr := ""
						sourceIdx := instruction.Accounts[0]
						// Try to found in PreTokenBalances
						if tx.Meta != nil {
							for _, balance := range tx.Meta.PreTokenBalances {
								if balance.AccountIndex == sourceIdx && balance.Owner != nil {
									fromAddr = balance.Owner.String()
									break
								}
							}
						}
						// Fallback to RPC, if sender not found in metadata
						if fromAddr == "" && int(sourceIdx) < len(msg.AccountKeys) {
							if err := limiter.Wait(cancelCtx); err != nil {
								return
							}

							// Get sender token account info
							sourceAccountInfo, err := sol.Client.GetAccountInfo(cancelCtx, msg.AccountKeys[sourceIdx])
							if err == nil && sourceAccountInfo != nil && sourceAccountInfo.Value != nil {
								var sourceTokenData token.Account
								// Parse token account data
								if err := bin.NewBinDecoder(sourceAccountInfo.Value.Data.GetBinary()).Decode(&sourceTokenData); err == nil {
									fromAddr = sourceTokenData.Owner.String()
								}
							}
						}

						// Get converted amount
						tokenAmount := binary.LittleEndian.Uint64(instruction.Data[1:9])
						amount := utils.RemoveDecimals(big.NewInt(int64(tokenAmount)), decimalsValue)

						localTxList = append(localTxList, &InboundTransaction{
							UserId:   userId,
							Currency: tokenSymbol,
							TxId:     txHash,
							From:     fromAddr,
							To:       ownerPubKey.String(),
							Amount:   amount,
						})
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
	for _, blockNum := range blocks {
		if _, ok := successfulBlocks.Load(blockNum); ok {
			lastContinuousBlock = blockNum
		} else {
			log.Printf("[%s] Block %d was not processed successfully, stopping at %d: %v",
				sol.NetworkCode, blockNum, lastContinuousBlock, firstErr)
			break
		}
	}

	// Save last processed block in Redis
	if lastContinuousBlock > lastBlockNumber {
		err = redis.GetClient().HSet(ctx, "last_blocks", sol.NetworkCode, lastContinuousBlock)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save last block in Redis: %w", sol.NetworkCode, err)
		}
	}

	log.Printf("[%s] Processing finished successfully: processed %d blocks, found %d user transactions",
		sol.NetworkCode, lastContinuousBlock-lastBlockNumber, len(txList))
	return txList, nil
}

/* GetUserWallet private methods group */
// @private getWallet Get HD-wallet by index
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// Returns: Derived address and private key
func (sol *Solana) getWallet(wType string, wId *uint32) (string, string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + sol.NetworkCode)
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

	return sol.deriveWallet(mnemonic, index)
}

// @private deriveWallet Derive HD-wallet by derivation index from master wallet mnemonic
// mnemonic: Master wallet mnemonic
// userId: Unique user id for derivation index
// Returns: Derived address and private key
func (sol *Solana) deriveWallet(mnemonic string, userId uint32) (string, string, error) {
	// Convert mnemonic to seed
	seed := bip39.NewSeed(mnemonic, "")

	// Init master HD key (SLIP-0010 uses "ed25519 seed")
	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	mac.Write(seed)
	result := mac.Sum(nil)

	currentKey := result[:32]
	currentChainCode := result[32:]

	// BIP44 derivation path: m/44'/501'/0'/0'/{userId}'
	path := []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 501,
		hdkeychain.HardenedKeyStart + 0,
		hdkeychain.HardenedKeyStart + 0,
		hdkeychain.HardenedKeyStart + userId,
	}

	// Derive child keys (all hardened for Ed25519)
	for _, index := range path {
		data := make([]byte, 37)
		data[0] = 0x00
		copy(data[1:33], currentKey)
		binary.BigEndian.PutUint32(data[33:], index)

		h := hmac.New(sha512.New, currentChainCode)
		h.Write(data)
		result := h.Sum(nil)

		currentKey = result[:32]
		currentChainCode = result[32:]
	}

	// Create Ed25519 keys
	privateKey := ed25519.NewKeyFromSeed(currentKey)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	// Create Solana address and private key (in base58)
	solanaPublicKey := solana.PublicKeyFromBytes(publicKey)
	solanaPrivateKey := solana.PrivateKey(privateKey)

	return solanaPublicKey.String(), solanaPrivateKey.String(), nil
}

/* GetMasterWallet private methods group */
// @private getNativeBalance Get wallet SOL balance
// addr: Wallet address
// Returns: SOL wallet balance
func (sol *Solana) getNativeBalance(addr string) (float64, error) {
	ctx := context.Background()

	// Parse address
	publicKey, err := solana.PublicKeyFromBase58(addr)
	if err != nil {
		return 0, fmt.Errorf("invalid address: %w", err)
	}

	// Get balance
	result, err := sol.Client.GetBalance(ctx, publicKey, rpc.CommitmentFinalized)
	if err != nil {
		return 0, fmt.Errorf("failed to get balance: %w", err)
	}

	// Get SOL decimals from config
	solDecimals := config.TokenSettings[sol.NetworkCode]["SOL"].Decimals
	// Get converted balance (in SOL)
	balance := utils.RemoveDecimals(big.NewInt(int64(result.Value)), int64(solDecimals))

	return balance, nil
}

// @private getSplBalance Get wallet SPL token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: SPL token wallet balance
func (sol *Solana) getSplBalance(contractAddr, addr string) (float64, error) {
	ctx := context.Background()

	// Parse address
	ownerPubKey, err := solana.PublicKeyFromBase58(addr)
	if err != nil {
		return 0, fmt.Errorf("invalid owner address: %w", err)
	}

	// Parse contract (mint) address
	mintPubKey, err := solana.PublicKeyFromBase58(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid contract address: %w", err)
	}

	// Get token accounts by owner
	result, err := sol.Client.GetTokenAccountsByOwner(
		ctx,
		ownerPubKey,
		&rpc.GetTokenAccountsConfig{
			Mint: &mintPubKey,
		},
		&rpc.GetTokenAccountsOpts{
			Encoding:   solana.EncodingBase64,
			Commitment: rpc.CommitmentFinalized,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get token accounts: %w", err)
	}

	// If no token accounts found, balance is 0
	if result == nil || len(result.Value) == 0 {
		return 0, nil
	}

	// Parse token account data to get balance
	var totalBalance uint64 = 0
	for _, account := range result.Value {
		var tokenAccount token.Account
		decoder := bin.NewBinDecoder(account.Account.Data.GetBinary())
		err = tokenAccount.UnmarshalWithDecoder(decoder)
		if err != nil {
			continue
		}
		totalBalance += tokenAccount.Amount
	}

	// Get token decimals from contract (mint)
	mintAccount, err := sol.Client.GetAccountInfo(ctx, mintPubKey)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract info: %w", err)
	}

	// Parse contract (mint) data
	var mintData token.Mint
	mintDecoder := bin.NewBinDecoder(mintAccount.Value.Data.GetBinary())
	err = mintData.UnmarshalWithDecoder(mintDecoder)
	if err != nil {
		return 0, fmt.Errorf("failed to parse contract data: %w", err)
	}

	// Get converted balance
	balance := utils.RemoveDecimals(big.NewInt(int64(totalBalance)), int64(mintData.Decimals))

	return balance, nil
}

/* GetTransactionCommission private methods group */
// @private calcNativeFee Calc fee for SOL transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in SOL
func (sol *Solana) calcNativeFee(fromAddr, toAddr string, amount float64) (float64, error) {
	ctx := context.Background()

	// Parse from and to address
	fromPubKey, err := solana.PublicKeyFromBase58(fromAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid from address: %w", err)
	}
	toPubKey, err := solana.PublicKeyFromBase58(toAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid to address: %w", err)
	}

	// Get SOL decimals from config
	solDecimals := config.TokenSettings[sol.NetworkCode]["SOL"].Decimals
	// Get converted amount (in lamports)
	lamports := utils.ApplyDecimals(amount, int64(solDecimals))

	// Get latest blockhash
	recentBlockhash, err := sol.Client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return 0, fmt.Errorf("failed to get recent blockhash: %w", err)
	}

	// Create transfer instruction
	transferInstruction := system.NewTransferInstruction(
		lamports.Uint64(),
		fromPubKey,
		toPubKey,
	).Build()

	// Create transaction
	tx, err := solana.NewTransaction(
		[]solana.Instruction{transferInstruction},
		recentBlockhash.Value.Blockhash,
		solana.TransactionPayer(fromPubKey),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create transaction: %w", err)
	}

	// Marshal message
	message, err := tx.Message.MarshalBinary()
	if err != nil {
		return 0, fmt.Errorf("failed to marshal message: %w", err)
	}
	// Get fee for message
	messageBase64 := base64.StdEncoding.EncodeToString(message)
	feeResult, err := sol.Client.GetFeeForMessage(ctx, messageBase64, rpc.CommitmentFinalized)
	if err != nil || feeResult.Value == nil {
		return 0, fmt.Errorf("failed to get fee: %w", err)
	}

	// Get converted amount (in SOL)
	convertedFee := utils.RemoveDecimals(big.NewInt(int64(*feeResult.Value)), int64(solDecimals))

	return convertedFee, nil
}

// @private calcSplFee Calc fee for SPL token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in SOL
func (sol *Solana) calcSplFee(currency, fromAddr, toAddr string, amount float64) (float64, error) {
	ctx := context.Background()
	currency = strings.ToUpper(currency)

	// Parse addresses
	fromPubKey, err := solana.PublicKeyFromBase58(fromAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid from address: %w", err)
	}
	toPubKey, err := solana.PublicKeyFromBase58(toAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid to address: %w", err)
	}

	// Get token contract address
	contractAddr, ok := config.GetContractAddress(sol.NetworkCode, currency)
	if !ok {
		return 0, fmt.Errorf("contract address not found in config: %s/%s", sol.NetworkCode, currency)
	}

	// Parse contract (mint) address
	mintPubKey, err := solana.PublicKeyFromBase58(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid contract address: %w", err)
	}

	// Get SOL decimals from config
	solDecimals := config.TokenSettings[sol.NetworkCode]["SOL"].Decimals

	// Get token decimals from contract (mint)
	mintAccount, err := sol.Client.GetAccountInfo(ctx, mintPubKey)
	if err != nil {
		return 0, fmt.Errorf("failed to get mint info: %w", err)
	}

	// Parse contract (mint) data
	var mintData token.Mint
	mintDecoder := bin.NewBinDecoder(mintAccount.Value.Data.GetBinary())
	err = mintData.UnmarshalWithDecoder(mintDecoder)
	if err != nil {
		return 0, fmt.Errorf("failed to parse mint data: %w", err)
	}

	// Get converted amount (in token decimals)
	tokenAmount := utils.ApplyDecimals(amount, int64(mintData.Decimals))

	// Get from and to associated token accounts (ATA) with fromPubKey and toPubKey
	fromAta, _, err := solana.FindAssociatedTokenAddress(fromPubKey, mintPubKey)
	if err != nil {
		return 0, fmt.Errorf("failed to find from ATA: %w", err)
	}
	toAta, _, err := solana.FindAssociatedTokenAddress(toPubKey, mintPubKey)
	if err != nil {
		return 0, fmt.Errorf("failed to find to ATA: %w", err)
	}

	// Get latest blockhash
	recentBlockhash, err := sol.Client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return 0, fmt.Errorf("failed to get recent blockhash: %w", err)
	}

	// Build instructions
	instructions := make([]solana.Instruction, 0)

	// Check if destination ATA exists and add create instruction
	_, err = sol.Client.GetAccountInfo(ctx, toAta)
	if err != nil {
		// Need to create ATA
		createAtaInstruction := ata.NewCreateInstruction(
			fromPubKey,
			toPubKey,
			mintPubKey,
		).Build()
		instructions = append(instructions, createAtaInstruction)
	}

	// Add transfer instruction
	transferInstruction := token.NewTransferInstruction(
		tokenAmount.Uint64(),
		fromAta,
		toAta,
		fromPubKey,
		[]solana.PublicKey{},
	).Build()
	instructions = append(instructions, transferInstruction)

	// Create transaction
	tx, err := solana.NewTransaction(
		instructions,
		recentBlockhash.Value.Blockhash,
		solana.TransactionPayer(fromPubKey),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create transaction: %w", err)
	}

	// Marshal message
	message, err := tx.Message.MarshalBinary()
	if err != nil {
		return 0, fmt.Errorf("failed to marshal message: %w", err)
	}

	// Get fee for message
	messageBase64 := base64.StdEncoding.EncodeToString(message)
	feeResult, err := sol.Client.GetFeeForMessage(ctx, messageBase64, rpc.CommitmentFinalized)
	if err != nil || feeResult.Value == nil {
		return 0, fmt.Errorf("failed to get fee: %w", err)
	}

	// Get converted amount (in SOL)
	convertedFee := utils.RemoveDecimals(big.NewInt(int64(*feeResult.Value)), int64(solDecimals))

	// Add rent exemption if creating new account, if needed
	if len(instructions) > 1 {
		rentExemption, err := sol.Client.GetMinimumBalanceForRentExemption(ctx, 165, rpc.CommitmentFinalized)
		if err == nil {
			rentFee := utils.RemoveDecimals(big.NewInt(int64(rentExemption)), int64(solDecimals))
			convertedFee += rentFee
		}
	}

	return convertedFee, nil
}

/* SendTransaction private methods group */
// @private sendNative Send native SOL transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// privateKey: Wallet private key
// Returns: Transaction hash
func (sol *Solana) sendNative(fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	ctx := context.Background()

	// Get SOL balance
	solBalance, err := sol.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get SOL balance: %w", err)
	}
	// Get tx fee (in SOL)
	txFee, err := sol.calcNativeFee(fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get SOL tx fee: %w", err)
	}
	// Check SOL balance before sending
	if solBalance < amount+txFee {
		return "", fmt.Errorf("insufficient balance: have %f %s, need %f + %f %s", solBalance, "SOL", amount, txFee, "SOL")
	}

	// Parse private key
	privateKeyBytes, err := hex.DecodeString(privateKey)
	if err != nil {
		return "", fmt.Errorf("invalid private key format: %w", err)
	}
	solanaPrivateKey := solana.PrivateKey(privateKeyBytes)

	// Parse from and to address
	fromPubKey, err := solana.PublicKeyFromBase58(fromAddr)
	if err != nil {
		return "", fmt.Errorf("invalid from address: %w", err)
	}
	toPubKey, err := solana.PublicKeyFromBase58(toAddr)
	if err != nil {
		return "", fmt.Errorf("invalid to address: %w", err)
	}

	// Get SOL decimals from config
	solDecimals := config.TokenSettings[sol.NetworkCode]["SOL"].Decimals
	// Get converted amount (in lamports)
	lamports := utils.ApplyDecimals(amount, int64(solDecimals))

	// Get latest blockhash
	recentBlockhash, err := sol.Client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to get recent blockhash: %w", err)
	}

	// Create transfer instruction
	transferInstruction := system.NewTransferInstruction(
		lamports.Uint64(),
		fromPubKey,
		toPubKey,
	).Build()

	// Create transaction
	tx, err := solana.NewTransaction(
		[]solana.Instruction{transferInstruction},
		recentBlockhash.Value.Blockhash,
		solana.TransactionPayer(fromPubKey),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create transaction: %w", err)
	}

	// Sign and send transaction to the network
	return sol.signAndSend(tx, fromPubKey, solanaPrivateKey)
}

// @private sendSpl Send SPL token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// privateKey: Wallet private key
// Returns: Transaction hash
func (sol *Solana) sendSpl(currency, fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	ctx := context.Background()
	currency = strings.ToUpper(currency)

	// Get token contract address
	contractAddr, ok := config.GetContractAddress(sol.NetworkCode, currency)
	if !ok {
		return "", fmt.Errorf("contract address not found in config: %s/%s", sol.NetworkCode, currency)
	}

	// Get SOL and token balances
	solBalance, err := sol.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get SOL balance: %w", err)
	}
	splBalance, err := sol.getSplBalance(contractAddr, fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get %s balance: %w", currency, err)
	}
	// Get tx fee (in SOL)
	txFee, err := sol.calcSplFee(currency, fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get %s tx fee: %w", currency, err)
	}
	// Check SOL and currency balances before sending
	if solBalance < txFee {
		return "", fmt.Errorf("insufficient SOL balance: have %f %s, need %f %s", solBalance, "SOL", txFee, "SOL")
	}
	if splBalance < amount {
		return "", fmt.Errorf("insufficient currency balance: have %f %s, need %f %s", splBalance, currency, amount, currency)
	}

	// Parse private key
	privateKeyBytes, err := hex.DecodeString(privateKey)
	if err != nil {
		return "", fmt.Errorf("invalid private key format: %w", err)
	}
	solanaPrivateKey := solana.PrivateKey(privateKeyBytes)

	// Parse from and to address
	fromPubKey, err := solana.PublicKeyFromBase58(fromAddr)
	if err != nil {
		return "", fmt.Errorf("invalid from address: %w", err)
	}
	toPubKey, err := solana.PublicKeyFromBase58(toAddr)
	if err != nil {
		return "", fmt.Errorf("invalid to address: %w", err)
	}

	// Parse contract (mint) address
	mintPubKey, err := solana.PublicKeyFromBase58(contractAddr)
	if err != nil {
		return "", fmt.Errorf("invalid contract address: %w", err)
	}

	// Get token decimals from contract (mint)
	mintAccount, err := sol.Client.GetAccountInfo(ctx, mintPubKey)
	if err != nil {
		return "", fmt.Errorf("failed to get mint info: %w", err)
	}

	// Parse contract (mint) data
	var mintData token.Mint
	mintDecoder := bin.NewBinDecoder(mintAccount.Value.Data.GetBinary())
	err = mintData.UnmarshalWithDecoder(mintDecoder)
	if err != nil {
		return "", fmt.Errorf("failed to parse mint data: %w", err)
	}

	// Convert amount to token units
	tokenAmount := utils.ApplyDecimals(amount, int64(mintData.Decimals))

	// Get from and to associated token accounts (ATA) with fromPubKey and toPubKey
	fromAta, _, err := solana.FindAssociatedTokenAddress(fromPubKey, mintPubKey)
	if err != nil {
		return "", fmt.Errorf("failed to find from ATA: %w", err)
	}
	toAta, _, err := solana.FindAssociatedTokenAddress(toPubKey, mintPubKey)
	if err != nil {
		return "", fmt.Errorf("failed to find to ATA: %w", err)
	}

	// Get latest blockhash
	recentBlockhash, err := sol.Client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to get recent blockhash: %w", err)
	}

	// Build instructions
	instructions := make([]solana.Instruction, 0)

	// Check if destination ATA exists and add create instruction
	_, err = sol.Client.GetAccountInfo(ctx, toAta)
	if err != nil {
		// Need to create ATA
		createAtaInstruction := ata.NewCreateInstruction(
			fromPubKey,
			toPubKey,
			mintPubKey,
		).Build()
		instructions = append(instructions, createAtaInstruction)
	}

	// Add transfer instruction
	transferInstruction := token.NewTransferInstruction(
		tokenAmount.Uint64(),
		fromAta,
		toAta,
		fromPubKey,
		[]solana.PublicKey{},
	).Build()
	instructions = append(instructions, transferInstruction)

	// Create transaction
	tx, err := solana.NewTransaction(
		instructions,
		recentBlockhash.Value.Blockhash,
		solana.TransactionPayer(fromPubKey),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create transaction: %w", err)
	}

	// Sign and send transaction to the network
	return sol.signAndSend(tx, fromPubKey, solanaPrivateKey)
}

// @private signAndSend Sign and send transaction to the network
// tx: Transaction struct
// publicKey: Wallet public key
// privateKey: Wallet private key
// Returns: Transaction hash
func (sol *Solana) signAndSend(tx *solana.Transaction, publicKey solana.PublicKey, privateKey solana.PrivateKey) (string, error) {
	// Sign transaction
	_, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if publicKey.Equals(key) {
			return &privateKey
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	sig, err := sol.Client.SendTransactionWithOpts(
		context.Background(),
		tx,
		rpc.TransactionOpts{
			SkipPreflight:       false,
			PreflightCommitment: rpc.CommitmentFinalized,
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	// Return transaction hash
	return sig.String(), nil
}
