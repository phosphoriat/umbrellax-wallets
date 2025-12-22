package chains

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
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

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	ata "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
)

type Solana struct {
	BaseChainService
	Client          *rpc.Client
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

	// Create RPC client
	client := rpc.New(cfg.SolRpcUrl)

	networkCode := "solana"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	return &Solana{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
		},
		Client: client,
	}
}

// GetCode Get network code
func (sol *Solana) GetCode() string {
	return sol.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
// userId: Unique user id
func (sol *Solana) GetUserWallet(userId int) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + sol.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	// Derive user address from master private key
	derivedAddr, err := sol.deriveAddress(masterData["privateKey"].(string), uint32(userId))
	if err != nil {
		return "", err
	}

	return derivedAddr, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
func (sol *Solana) GetMasterWallet() (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + sol.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Get SPL token balances
	balances := make(map[string]any)
	for tokenName, contractAddr := range config.ContractsAddresses[sol.NetworkCode].(map[string]string) {
		tokenBalance, _ := sol.getSplBalance(contractAddr, masterAddr)
		balances[tokenName] = tokenBalance
	}

	// Get SOL native balance
	solBalance, _ := sol.getNativeBalance(masterAddr)
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
func (sol *Solana) GetTransactionCommission(oType, wType string, wId *string, currency, toAddr string, amount float64, memo string) (map[string]float64, error) {
	var walletPath string
	if wType == "master" {
		walletPath = "/master/" + sol.NetworkCode
	} else {
		if wId == nil {
			return nil, fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, sol.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet address
	fromAddr, ok := walletData["address"].(string)
	if !ok || fromAddr == "" {
		return nil, fmt.Errorf("invalid or missing %s wallet address in Vault", sol.NetworkCode)
	}

	// Calc chain fee for currency (in SOL value)
	var chainFee float64
	switch currency {
	case "SOL":
		chainFee, err = sol.calcNativeFee(fromAddr, toAddr, amount)
	default:
		chainFee, err = sol.calcSplFee(currency, fromAddr, toAddr, amount)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction commission: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetHTTPClient().GetWithdrawalFee(currency, sol.NetworkCode)

		// Get AML check cost from config (in USD value)
		cfg, _ := config.Load()
		amlFee, _ = strconv.ParseFloat(cfg.AmlCheckCost, 64)
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
func (sol *Solana) SendTransaction(wType string, wId *string, currency, toAddr string, amount float64, memo string) (string, error) {
	var walletPath string
	if wType == "master" {
		walletPath = fmt.Sprintf("/master/%s", sol.NetworkCode)
	} else {
		if wId == nil {
			return "", fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, sol.NetworkCode)
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
		return "", fmt.Errorf("invalid or missing %s wallet data in Vault", sol.NetworkCode)
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "SOL":
		hash, err = sol.sendNative(fromAddr, toAddr, amount, privateKey)
	default:
		hash, err = sol.sendSpl(currency, fromAddr, toAddr, amount, privateKey)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, nil
}

// GetInboundTransactions Get new inbound transactions list
func (sol *Solana) GetInboundTransactions() ([]*InboundTransaction, error) {
	ctx := context.Background()

	// Get current block (slot) number
	currentBlockNum, err := sol.Client.GetSlot(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get current slot: %w", sol.NetworkCode, err)
	}

	// Get last block number from Redis (or use current block number, if not exists)
	lastBlock, err := redis.GetClient().HGet(ctx, "last_blocks", sol.NetworkCode)
	var lastBlockNumber uint64
	if err != nil || lastBlock == "" {
		lastBlockNumber = currentBlockNum - 10
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

	// Get SOL decimals from config
	solDecimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", sol.NetworkCode, "SOL"),
	)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get SOL decimals from config: %w", sol.NetworkCode, err)
	}

	// Wait group to wait for all address goroutines to complete
	var wg sync.WaitGroup
	// Mutex for collecting results
	var resultMu sync.Mutex
	// Collect all transactions
	allTxList := make([]*InboundTransaction, 0)

	log.Printf("[%s] Processing transactions for %d addresses from block %d to %d",
		sol.NetworkCode, len(addressList), lastBlockNumber, currentBlockNum)

	// Process each address from list
	for address, userId := range addressList {
		wg.Add(1)

		go func() {
			defer wg.Done()

			// Parse address
			publicKey, err := solana.PublicKeyFromBase58(address)
			if err != nil {
				log.Printf("[%s] Failed to parse address '%s': %v", sol.NetworkCode, address, err)
				return
			}

			// Get signatures for address
			signatures, err := sol.Client.GetSignaturesForAddress(ctx, publicKey)
			if err != nil {
				log.Printf("[%s] Failed to get signatures for address '%s': %v", sol.NetworkCode, address, err)
				return
			}

			log.Printf("[%s] Processing %d signatures for address '%s'",
				sol.NetworkCode, len(signatures), address)

			localTxList := make([]*InboundTransaction, 0)

			// Process signatures
			for _, sig := range signatures {
				// Skip if signature slot is before our last processed slot
				if sig.Slot <= lastBlockNumber {
					continue
				}
				// Skip if signature slot is after current slot
				if sig.Slot > currentBlockNum {
					continue
				}
				// Skip failed transactions
				if sig.Err != nil {
					continue
				}

				// Get full transaction
				tx, err := sol.Client.GetTransaction(ctx, sig.Signature, &rpc.GetTransactionOpts{
					Encoding:   solana.EncodingBase64,
					Commitment: rpc.CommitmentFinalized,
				})
				if err != nil {
					log.Printf("[%s] Failed to get transaction '%s': %v",
						sol.NetworkCode, sig.Signature, err)
					continue
				}
				// Skip if transaction is nil
				if tx == nil || tx.Transaction == nil {
					continue
				}

				txHash := sig.Signature.String()

				// Parse transaction
				parsedTx, err := tx.Transaction.GetTransaction()
				if err != nil {
					log.Printf("[%s] Failed to parse transaction '%s': %v", sol.NetworkCode, txHash, err)
					continue
				}

				// Get message
				msg := parsedTx.Message

				// Process all instructions in transaction
				for _, instruction := range msg.Instructions {
					// Get program id
					programId := msg.AccountKeys[instruction.ProgramIDIndex]

					// Check if it's native SOL transfer
					if programId.Equals(solana.SystemProgramID) {
						// Check if it's system transfer instruction (type 2)
						if len(instruction.Data) > 0 && instruction.Data[0] == 2 {
							// Get transfer accounts ([source, destination, owner])
							if len(instruction.Accounts) < 2 {
								log.Printf("[%s] Failed to get transfer accounts data for tx '%s': %v",
									sol.NetworkCode, txHash, err)
								continue
							}
							fromIdx := instruction.Accounts[0]
							toIdx := instruction.Accounts[1]

							// Get from and to address
							fromAddr := msg.AccountKeys[fromIdx].String()
							toAddr := msg.AccountKeys[toIdx].String()

							// Check if receiver is our address
							if toAddr == address {
								// Decode amount (bytes 1-9 are lamports as little-endian uint64)
								if len(instruction.Data) < 9 {
									log.Printf("[%s] Failed to decode amount for tx '%s': %v",
										sol.NetworkCode, txHash, err)
									continue
								}

								// Parse lamports (uint64 little-endian)
								lamports := uint64(instruction.Data[1]) |
									uint64(instruction.Data[2])<<8 |
									uint64(instruction.Data[3])<<16 |
									uint64(instruction.Data[4])<<24 |
									uint64(instruction.Data[5])<<32 |
									uint64(instruction.Data[6])<<40 |
									uint64(instruction.Data[7])<<48 |
									uint64(instruction.Data[8])<<56

								// Get converted amount (in SOL)
								amount := utils.RemoveDecimals(big.NewInt(int64(lamports)), int64(solDecimals))

								localTxList = append(localTxList, &InboundTransaction{
									UserId:   int(userId),
									Currency: "SOL",
									TxId:     txHash,
									From:     fromAddr,
									To:       toAddr,
									Amount:   amount,
								})
							}
						}
					}

					// Check if it's SPL token transfer
					if programId.Equals(solana.TokenProgramID) {
						// Check, if it's transfer instruction (type 3)
						if len(instruction.Data) > 0 && instruction.Data[0] == 3 {
							// Get transfer accounts ([source, destination, owner])
							if len(instruction.Accounts) < 2 {
								log.Printf("[%s] Failed to get transfer accounts data for tx '%s': %v",
									sol.NetworkCode, txHash, err)
								continue
							}

							sourceIdx := instruction.Accounts[0]
							destIdx := instruction.Accounts[1]

							// Get destination token account info
							destTokenAccount, err := sol.Client.GetAccountInfo(ctx, msg.AccountKeys[destIdx])
							if err != nil || destTokenAccount == nil || destTokenAccount.Value == nil {
								log.Printf("[%s] Failed to get account info for tx '%s': %v",
									sol.NetworkCode, txHash, err)
								continue
							}

							// Parse token account data
							var destTokenData token.Account
							decoder := bin.NewBinDecoder(destTokenAccount.Value.Data.GetBinary())
							err = destTokenData.UnmarshalWithDecoder(decoder)
							if err != nil {
								log.Printf("[%s] Failed to unmarshal destination token data for tx '%s': %v",
									sol.NetworkCode, txHash, err)
								continue
							}

							// Get to address
							toAddr := destTokenData.Owner.String()

							// Check if to address equals current address from list
							if toAddr == address {
								// Parse amount (bytes 1-9 are amount as little-endian uint64)
								if len(instruction.Data) < 9 {
									continue
								}

								tokenAmount := uint64(instruction.Data[1]) |
									uint64(instruction.Data[2])<<8 |
									uint64(instruction.Data[3])<<16 |
									uint64(instruction.Data[4])<<24 |
									uint64(instruction.Data[5])<<32 |
									uint64(instruction.Data[6])<<40 |
									uint64(instruction.Data[7])<<48 |
									uint64(instruction.Data[8])<<56

								// Get mint address (token contract address)
								contractAddr := destTokenData.Mint.String()

								// Get token symbol from config
								tokenSymbol, err := config.GetTokenByContractAddr(sol.NetworkCode, contractAddr)
								if err != nil {
									log.Printf("[%s] Unknown token contract address '%s'", sol.NetworkCode, contractAddr)
									continue
								}

								// Get contract decimals from cache or fetch from contract
								sol.decimalsCacheMu.RLock()
								cached, exists := sol.decimalsCache[contractAddr]
								sol.decimalsCacheMu.RUnlock()

								var decimalsValue int64
								if exists {
									// Get contract decimals from cache
									decimalsValue = cached
								} else {
									// Get contract info
									mintPubKey, _ := solana.PublicKeyFromBase58(contractAddr)
									mintAccount, err := sol.Client.GetAccountInfo(ctx, mintPubKey)
									if err != nil || mintAccount == nil || mintAccount.Value == nil {
										log.Printf("[%s] Failed to get contract info for '%s': %v",
											sol.NetworkCode, contractAddr, err)
										continue
									}

									// Parse contract data
									var mintData token.Mint
									mintDecoder := bin.NewBinDecoder(mintAccount.Value.Data.GetBinary())
									err = mintData.UnmarshalWithDecoder(mintDecoder)
									if err != nil {
										log.Printf("[%s] Failed to parse mint data for '%s': %v",
											sol.NetworkCode, contractAddr, err)
										continue
									}

									decimalsValue = int64(mintData.Decimals)

									// Save decimals to cache
									sol.decimalsCacheMu.Lock()
									sol.decimalsCache[contractAddr] = decimalsValue
									sol.decimalsCacheMu.Unlock()
								}

								// Get converted amount
								convertedAmount := utils.RemoveDecimals(big.NewInt(int64(tokenAmount)), decimalsValue)

								// Get sender token account info
								sourceTokenAccount, err := sol.Client.GetAccountInfo(ctx, msg.AccountKeys[sourceIdx])
								if err != nil || sourceTokenAccount == nil || sourceTokenAccount.Value == nil {
									log.Printf("[%s] Failed to get sender token account info for tx '%s': %v",
										sol.NetworkCode, txHash, err)
									continue
								}

								// Parse token account data
								var sourceTokenData token.Account
								sourceDecoder := bin.NewBinDecoder(sourceTokenAccount.Value.Data.GetBinary())
								err = sourceTokenData.UnmarshalWithDecoder(sourceDecoder)
								if err != nil {
									log.Printf("[%s] Failed to unmarshal sender token account data for tx '%s': %v",
										sol.NetworkCode, txHash, err)
									continue
								}

								// Get from address
								fromAddr := sourceTokenData.Owner.String()

								localTxList = append(localTxList, &InboundTransaction{
									UserId:   int(userId),
									Currency: tokenSymbol,
									TxId:     txHash,
									From:     fromAddr,
									To:       address,
									Amount:   convertedAmount,
								})
							}
						}
					}
				}
			}

			// Add to global result
			if len(localTxList) > 0 {
				resultMu.Lock()
				allTxList = append(allTxList, localTxList...)
				resultMu.Unlock()
			}
		}()
	}

	// Wait for all goroutines
	wg.Wait()

	// Save current block as last processed
	if currentBlockNum > lastBlockNumber {
		err = redis.GetClient().HSet(ctx, "last_blocks", sol.NetworkCode, currentBlockNum)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save last block in Redis: %w", sol.NetworkCode, err)
		}
	}

	log.Printf("[%s] Processed %d transactions successful", sol.NetworkCode, len(allTxList))

	return allTxList, nil
}

/* GetUserWallet private methods group */
// @private deriveAddress Derive HD-wallet address by derivation index from master wallet private key
// masterPrivateKey: Master wallet private key
// userId: Unique user id for derivation index
func (sol *Solana) deriveAddress(masterPrivateKey string, userId uint32) (string, error) {
	// Get seed bytes from private key
	seed, err := hex.DecodeString(masterPrivateKey)
	if err != nil {
		return "", fmt.Errorf("invalid master key format: %w", err)
	}

	// Derive private key
	_, publicKey := sol.deriveEd25519Key(seed, userId)

	// Create Solana wallet from public key
	solanaPublicKey := solana.PublicKeyFromBytes(publicKey)

	// Return the Solana address (base58 encoded)
	return solanaPublicKey.String(), nil
}

// @private deriveEd25519Key Solana Ed25519 keys derivation
func (sol *Solana) deriveEd25519Key(seed []byte, userId uint32) (ed25519.PrivateKey, ed25519.PublicKey) {
	// Init master HD key for BIP32-Ed25519
	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	mac.Write(seed)
	result := mac.Sum(nil)

	key := result[:32]
	chainCode := result[32:]

	// Solana uses derivation scheme based on HMAC-SHA512
	// Derivation path: m/44'/501'/0'/0'/{userId}'
	path := []uint32{
		0x8000002C,          // 44'
		0x800001F5,          // 501' (Solana coin type)
		0x80000000,          // 0'
		0x80000000,          // 0'
		0x80000000 + userId, // {userId}'
	}

	// Apply HMAC-SHA512 for each path level
	for _, index := range path {
		data := make([]byte, 37)
		data[0] = 0x00 // for hardened derivation
		copy(data[1:33], key)
		binary.BigEndian.PutUint32(data[33:], index)

		hmacHash := hmac.New(sha512.New, chainCode)
		hmacHash.Write(data)
		result := hmacHash.Sum(nil)

		key = result[:32]
		chainCode = result[32:]
	}

	// Create Ed25519 keys from the received seed
	privateKey := ed25519.NewKeyFromSeed(key)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	return privateKey, publicKey
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
	solDecimals, _ := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", sol.NetworkCode, "SOL"),
	)
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
	solDecimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", sol.NetworkCode, "SOL"),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get SOL decimals from config: %w", err)
	}

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
	feeResult, err := sol.Client.GetFeeForMessage(ctx, string(message), rpc.CommitmentFinalized)
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
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", sol.NetworkCode, currency),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract address for %s: %w", currency, err)
	}

	// Parse contract (mint) address
	mintPubKey, err := solana.PublicKeyFromBase58(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid contract address: %w", err)
	}

	// Get SOL decimals from config
	solDecimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", sol.NetworkCode, "SOL"),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get SOL decimals: %w", err)
	}

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
	feeResult, err := sol.Client.GetFeeForMessage(ctx, string(message), rpc.CommitmentFinalized)
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
	solDecimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", sol.NetworkCode, "SOL"),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get SOL decimals: %w", err)
	}

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
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", sol.NetworkCode, currency),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get contract address for %s: %w", currency, err)
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
