package chains

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/database/vault"
	"wallets/internal/repository"
	"wallets/internal/services/api/bybit"
	"wallets/internal/utils"

	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/jetton"
	"github.com/xssnick/tonutils-go/ton/wallet"
	"github.com/xssnick/tonutils-go/tvm/cell"
	"golang.org/x/time/rate"
)

type (
	Ton struct {
		BaseChainService
		Client        ton.APIClientWrapped
		RateLimit     int
		WalletVersion wallet.Version
		jettonCache   map[string]*JettonInfo
		jettonCacheMu sync.RWMutex
	}

	JettonInfo struct {
		masterAddr *address.Address
		decimals   int64
		token      string
	}
)

// Global package vars
var (
	tonInstance *Ton
	tonOnce     sync.Once
)

// GetTon Get Ethereum chain instance
func GetTon() *Ton {
	tonOnce.Do(func() {
		tonInstance = newTon()
	})
	return tonInstance
}

// @private newTon Create new instance of Ton (private constructor)
func newTon() *Ton {
	// Load config
	cfg, _ := config.Load()

	// Create new connection pool
	client := liteclient.NewConnectionPool()

	// Add liteservers from config to pool
	content, err := os.ReadFile(cfg.TonConfig)
	if err != nil {
		log.Fatalf("[Ton] Failed to read liteserver config: %v", err)
	}
	var tonConfig liteclient.GlobalConfig
	if err := json.Unmarshal(content, &tonConfig); err != nil {
		log.Fatalf("[Ton] Failed to parse liteserver config: %v", err)
	}
	err = client.AddConnectionsFromConfig(context.Background(), &tonConfig)
	if err != nil {
		log.Fatalf("[Ton] Failed to connect: %v", err)
	}

	// Create new Ton API client
	apiClient := ton.NewAPIClient(client).WithRetry(3)

	// Get network settings
	networkCode := "ton"
	nativeToken := config.NetworkSettings[networkCode].NativeToken

	return &Ton{
		BaseChainService: BaseChainService{
			UserRepo:    repository.NewUserRepository(),
			NetworkCode: networkCode,
			NativeToken: nativeToken,
		},
		Client:        apiClient,
		RateLimit:     10, // 10 for TonConsole 
		WalletVersion: wallet.V4R2,
		jettonCache:   make(map[string]*JettonInfo),
	}
}

// GetCode Get network code
func (ton *Ton) GetCode() string {
	return ton.NetworkCode
}

// GetUserWalletAddress Get user HD-wallet by user id
// @note In TON no user wallets, so this method returns master wallet address (for using MEMO tags)
// userId: Unique user id
// Returns: Master wallet address
func (ton *Ton) GetUserWalletAddress(userId uint32) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + ton.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return "", fmt.Errorf("master address not found in Vault")
	}

	return masterAddr, nil
}

// GetUserWallet Get user wallet info (address and token balances)
// @note In TON no user wallets, so this method returns null balances
// userId: Unique user id
// Returns: Master address and null balances
func (ton *Ton) GetUserWallet(userId uint32) (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + ton.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("master address not found in Vault")
	}

	return map[string]any{
		"address":  masterAddr,
		"balances": map[string]any{},
	}, nil
}

// GetUserWalletBalance Get token balance amount for user wallet
// @note In TON no user wallets, so this method returns null balance
// userId: User id
// currency: Token ticker
// Returns 0
func (ton *Ton) GetUserWalletBalance(userId uint32, currency string) (float64, error) {
	return 0, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of master wallet info (address and balances of all available tokens)
func (ton *Ton) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + ton.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("master address not found in Vault")
	}
	masterMnemonic, ok := masterData["mnemonic"].(string)
	if !ok || masterMnemonic == "" {
		return nil, fmt.Errorf("master mnemonic not found in Vault")
	}
	seedWords := strings.Split(masterMnemonic, " ")

	// Init wallet instance
	walletInstance, err := wallet.FromSeedWithOptions(ton.Client, seedWords, ton.WalletVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to init wallet instance: %w", err)
	}

	// Get Jetton token balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[ton.NetworkCode] {
		tokenBalance, err := ton.getJettonBalance(token, contractAddr, masterAddr)
		if err != nil {
			continue
		}

		balances[token] = tokenBalance
	}

	// Get TON native balance
	tonBalance, err := ton.getNativeBalance(walletInstance)
	if err != nil {
		return nil, fmt.Errorf("failed to get native balance: %w", err)
	}
	balances[ton.NativeToken] = tonBalance

	return map[string]any{
		"address":  masterAddr,
		"balances": balances,
	}, nil
}

// GetTransactionCommission Get transaction commission
// oType: Operation type (withdrawal/inner)
// wType: Wallet type (always master in TON)
// wId: User id (not use in TON)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag
// Returns: Map of all fee types (chain_fee, bybit_fee, aml_fee)
func (ton *Ton) GetTransactionCommission(oType, wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (map[string]float64, error) {
	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret("master/" + ton.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet mnemonic
	mnemonic, ok := walletData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return nil, fmt.Errorf("invalid or missing %s wallet mnemonic in Vault", ton.NetworkCode)
	}
	seedWords := strings.Split(mnemonic, " ")

	// Init wallet instance
	walletInstance, err := wallet.FromSeedWithOptions(ton.Client, seedWords, ton.WalletVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize wallet: %w", err)
	}

	// Calc chain fee for currency
	var chainFee float64 = 0
	switch currency {
	case "TON":
		chainFee, err = ton.calcNativeFee(walletInstance, toAddr, amount, memo)
	default:
		chainFee, err = ton.calcJettonFee(walletInstance, currency, toAddr, amount, memo)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction commission: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Load config
		cfg, _ := config.Load()

		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetClient().GetWithdrawalFee(context.Background(), currency, ton.NetworkCode)
		// Get AML check cost from config (in USD value)
		amlFee = cfg.AmlCheckCost
	}

	return map[string]float64{
		"chain_fee": chainFee, // In TON
		"bybit_fee": bybitFee, // In currency value
		"aml_fee":   amlFee,   // in USD
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// toAddr: Recipient wallet address (required)
// Returns: Transaction status (success/pending)
func (ton *Ton) GetTransactionStatus(hash, toAddr string) (string, error) {
	if toAddr == "" {
		return "", fmt.Errorf("to address is required")
	}

	ctx := context.Background()

	// Parse to address
	to, err := address.ParseAddr(toAddr)
	if err != nil {
		return "", fmt.Errorf("invalid recipient address: %w", err)
	}
	// Decode hash for comparison
	hashBytes, err := base64.StdEncoding.DecodeString(hash)
	if err != nil {
		return "", fmt.Errorf("invalid transaction hash: %w", err)
	}

	// Get current masterchain info
	chainInfo, err := ton.Client.CurrentMasterchainInfo(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get masterchain info: %w", err)
	}

	// Get account info
	account, err := ton.Client.GetAccount(ctx, chainInfo, to)
	if err != nil {
		return "", fmt.Errorf("failed to get account info: %w", err)
	}

	// Init pagination params from account's last transaction
	lt := account.LastTxLT
	txHash := account.LastTxHash

	// Iterate transaction history (max 100 iterations)
	for i := 0; i < 100; i++ {
		// Get transaction batch (20 transaction per request)
		txs, err := ton.Client.ListTransactions(ctx, to, 20, lt, txHash)
		if err != nil {
			return "", fmt.Errorf("failed to list transactions: %w", err)
		}

		// No more transactions in history - not found, assuming still pending
		if len(txs) == 0 {
			return "pending", nil
		}

		// Search for matching hash in current batch
		for _, tx := range txs {
			if bytes.Equal(tx.Hash, hashBytes) {
				return "success", nil
			}
		}

		// Get the oldest transaction for next iteration
		oldest := txs[0]
		// Check for end of history (no progress) - transaction not found
		if oldest.LT == lt && bytes.Equal(oldest.Hash, txHash) {
			return "pending", nil
		}

		// Update pagination params
		lt, txHash = oldest.LT, oldest.Hash
	}

	// Max iterations reached - assuming still pending
	return "pending", nil
}

// SendTransaction Send transaction to the network
// wType: Wallet type (always master in TON)
// wId: User id (not use in TON)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (ton *Ton) SendTransaction(wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (string, error) {
	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret("master/" + ton.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet mnemonic
	mnemonic, okM := walletData["mnemonic"].(string)
	if !okM {
		return "", fmt.Errorf("invalid or missing %s wallet data in Vault", ton.NetworkCode)
	}

	// Init wallet instance
	seedWords := strings.Split(mnemonic, " ")
	walletInstance, err := wallet.FromSeedWithOptions(ton.Client, seedWords, ton.WalletVersion)
	if err != nil {
		return "", fmt.Errorf("failed to initialize wallet: %w", err)
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "TON":
		hash, err = ton.sendNative(walletInstance, toAddr, amount, memo)
	default:
		hash, err = ton.sendJetton(currency, walletInstance, toAddr, amount, memo)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, nil
}

// GetInboundTransactions Get new inbound transactions list
// Returns: Inbound transactions list
func (ton *Ton) GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error) {
	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(ton.RateLimit), 1)

	// Get last logical time (LT) from Redis
	lastLTStr, err := redis.GetClient().HGet(ctx, "last_blocks", ton.NetworkCode)
	var lastProcessedLT uint64
	if err != nil || lastLTStr == "" {
		lastProcessedLT = 0
		log.Printf("[%s] First run, starting from LT 0", ton.NetworkCode)
	} else {
		lastProcessedLT, _ = strconv.ParseUint(lastLTStr, 10, 64)
	}

	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + ton.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get master data: %w", ton.NetworkCode, err)
	}
	masterAddrStr := masterData["address"].(string)
	// Parse master address
	masterAddr := address.MustParseAddr(masterAddrStr)

	// Get TON decimals from config
	tonDecimals := config.TokenSettings[ton.NetworkCode]["TON"].Decimals

	// Get current block
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	block, err := ton.Client.CurrentMasterchainInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get masterchain info: %w", ton.NetworkCode, err)
	}

	// Get current account state
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	account, err := ton.Client.GetAccount(ctx, block, masterAddr)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get account: %w", ton.NetworkCode, err)
	}

	// No transactions on account yet
	if account.LastTxLT == 0 {
		return []*InboundTransaction{}, nil
	}

	// No new transactions since last check
	if account.LastTxLT <= lastProcessedLT {
		log.Printf("[%s] No new raw txs to search (lastLT: %d, accountLT: %d)",
			ton.NetworkCode, lastProcessedLT, account.LastTxLT)
		return []*InboundTransaction{}, nil
	}

	// Collect all new transactions with pagination
	var allNewTxs []*tlb.Transaction
	currentLT := account.LastTxLT
	currentHash := account.LastTxHash

	// Paginate backwards until we reach lastProcessedLT
	for currentLT > lastProcessedLT {
		// Get transactions list for master address (up to 100 transactions)
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
		txList, err := ton.Client.ListTransactions(ctx, masterAddr, 100, currentLT, currentHash)
		if err != nil {
			if strings.Contains(err.Error(), "no transactions were found") {
				log.Printf("[%s] No txs found yet at wallet", ton.NetworkCode)
				return []*InboundTransaction{}, nil
			}
			return nil, fmt.Errorf("[%s] Failed to get txs for master address: %w",
				ton.NetworkCode, err)
		}

		// Check if tx list is not empty
		if len(txList) == 0 {
			log.Printf("[%s] No new raw txs to search", ton.NetworkCode)
			return []*InboundTransaction{}, nil
		}

		// Filter only new transactions (LT > lastProcessedLT)
		for _, tx := range txList {
			if tx.LT > lastProcessedLT {
				allNewTxs = append(allNewTxs, tx)
			}
		}

		// Move to older transactions (oldest is first in result)
		oldest := txList[0]
		if oldest.PrevTxLT == 0 || oldest.LT <= lastProcessedLT {
			break
		}
		currentLT = oldest.PrevTxLT
		currentHash = oldest.PrevTxHash
	}

	if len(allNewTxs) == 0 {
		log.Printf("[%s] No new raw txs to search", ton.NetworkCode)
		return []*InboundTransaction{}, nil
	}

	log.Printf("[%s] Start searching %d new raw txs from LT %d to %d",
		ton.NetworkCode, len(allNewTxs), lastProcessedLT, account.LastTxLT)

	// Processed inbound transactions result list
	inboundTxList := make([]*InboundTransaction, 0)

	// Process transactions
	for _, tx := range allNewTxs {
		// Process only incoming internal messages
		if tx.IO.In == nil || tx.IO.In.MsgType != tlb.MsgTypeInternal {
			continue
		}

		// Safely convert message to InternalMessage
		intMsg := tx.IO.In.AsInternal()
		// Get transaction hash
		txHash := fmt.Sprintf("%x", tx.Hash)

		// Skip transaction, if message body is empty
		if intMsg.Body == nil {
			continue
		}

		// Parse message body once
		slice := intMsg.Body.BeginParse()
		// Check if at least 32 bits for op code
		if slice.BitsLeft() < 32 {
			continue
		}
		// Get op code
		opCode, err := slice.LoadUInt(32)
		if err != nil {
			continue
		}

		switch opCode {
		// If it's a native TON transfer
		case 0x00000000:
			// Extract comment
			comment, err := slice.LoadStringSnake()
			if err != nil {
				continue
			}
			comment = strings.TrimSpace(comment)
			if comment == "" {
				continue
			}

			// Get user id by comment (memo)
			userId, err := ton.UserRepo.GetUserIdByNumber(comment)
			if err != nil {
				log.Printf("[%s] Failed to get user id by memo '%s' in tx '%s': %v",
					ton.NetworkCode, comment, txHash, err)
				continue
			}

			// Check amount
			if intMsg.Amount.Nano().Uint64() == 0 {
				continue
			}

			// Get from address
			fromAddr := intMsg.SrcAddr.String()
			// Get converted amount (in TON)
			amount := utils.RemoveDecimals(intMsg.Amount.Nano(), int64(tonDecimals))

			inboundTxList = append(inboundTxList, &InboundTransaction{
				UserId:   userId,
				Currency: "TON",
				TxId:     txHash,
				From:     fromAddr,
				To:       masterAddr.String(),
				Amount:   amount,
			})

			log.Printf("[%s] %f TON raw tx '%s' found", ton.NetworkCode, amount, txHash)

		// If it's a Jetton transfer (0x7362d09c - internal_transfer to wallet)
		case 0x7362d09c:
			// Skip query_id (64 bits)
			if slice.BitsLeft() < 64 {
				continue
			}
			_, err = slice.LoadUInt(64)
			if err != nil {
				continue
			}

			// Read jetton amount
			jettonAmount, err := slice.LoadBigCoins()
			if err != nil {
				continue
			}

			// Read sender address
			jettonSender, err := slice.LoadAddr()
			if err != nil {
				continue
			}

			// Read forward payload for comment
			// Skip response_destination
			_, _ = slice.LoadAddr()
			// Skip custom_payload flag
			if slice.BitsLeft() > 0 {
				_, _ = slice.LoadUInt(1)
			}
			// Try to read forward_ton_amount
			_, _ = slice.LoadBigCoins()

			// Try to read comment from forward_payload
			var comment string
			if slice.RefsNum() > 0 {
				ref, err := slice.LoadRef()
				if err == nil {
					if ref.BitsLeft() >= 32 {
						op, _ := ref.LoadUInt(32)
						if op == 0 {
							comment, _ = ref.LoadStringSnake()
							comment = strings.TrimSpace(comment)
						}
					}
				}
			}
			// Check, if transaction has comment
			if comment == "" {
				log.Printf("[%s] Jetton tx '%s' has no comment", ton.NetworkCode, txHash)
				continue
			}

			// Get user by comment (memo)
			userId, err := ton.UserRepo.GetUserIdByNumber(comment)
			if err != nil {
				log.Printf("[%s] Failed to get user by memo '%s': %v", ton.NetworkCode, comment, err)
				continue
			}

			// Get jetton wallet address (message sender is jetton wallet)
			jettonWalletAddr := intMsg.SrcAddr.String()

			// Trying to get jetton info from cache
			ton.jettonCacheMu.RLock()
			jettonInfo, exists := ton.jettonCache[jettonWalletAddr]
			ton.jettonCacheMu.RUnlock()
			// Fetch from contract
			if !exists {
				if err := limiter.Wait(ctx); err != nil {
					return nil, err
				}

				// Get current masterchain info
				master, err := ton.Client.CurrentMasterchainInfo(ctx)
				if err != nil {
					log.Printf("[%s] Failed to get current masterchain info: %v", ton.NetworkCode, err)
					continue
				}

				// Call get_wallet_data method on jetton wallet
				// Returns stack: [balance, owner, jetton_master, jetton_wallet_code]
				result, err := ton.Client.RunGetMethod(ctx, master, intMsg.SrcAddr, "get_wallet_data")
				if err != nil {
					log.Printf("[%s] Failed to run get_wallet_data: %v", ton.NetworkCode, err)
					continue
				}

				// Get jetton master address from stack (index 2)
				// Stack in TON starts from 0: [0]=balance, [1]=owner, [2]=jetton_master, [3]=code
				jettonMasterSlice := result.MustSlice(2)
				jettonMasterAddr, err := jettonMasterSlice.LoadAddr()
				if err != nil {
					log.Printf("[%s] Failed to parse jetton master address: %v", ton.NetworkCode, err)
					continue
				}

				// Get token name by contract address (jetton master address)
				token, ok := config.GetTokenByContractAddr(ton.NetworkCode, jettonMasterAddr.String())
				if !ok {
					log.Printf("[%s] Unknown jetton master address '%s'", ton.NetworkCode, jettonMasterAddr.String())
					continue
				}

				// Get token decimals from config
				decimals := config.TokenSettings[ton.NetworkCode][token].Decimals

				jettonInfo = &JettonInfo{
					masterAddr: jettonMasterAddr,
					decimals:   int64(decimals),
					token:      token,
				}

				// Save jetton info to cache
				ton.jettonCacheMu.Lock()
				ton.jettonCache[jettonWalletAddr] = jettonInfo
				ton.jettonCacheMu.Unlock()
			}

			// Get converted amount (with removed decimals)
			amount := utils.RemoveDecimals(jettonAmount, jettonInfo.decimals)

			inboundTxList = append(inboundTxList, &InboundTransaction{
				UserId:   userId,
				Currency: jettonInfo.token,
				TxId:     txHash,
				From:     jettonSender.String(),
				To:       masterAddr.String(),
				Amount:   amount,
			})

			log.Printf("[%s] Jetton (%f %s) raw tx '%s' found",
				ton.NetworkCode, amount, jettonInfo.token, txHash)
		}
	}

	// Save max LT (latest transaction LT) as last processed in Redis
	err = redis.GetClient().HSet(ctx, "last_blocks", ton.NetworkCode, account.LastTxLT)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to save last LT in Redis: %w", ton.NetworkCode, err)
	}
	log.Printf("[%s] Saved last LT: %d", ton.NetworkCode, account.LastTxLT)

	log.Printf("[%s] Searching finished successfully, found [%d] user raw txs",
		ton.NetworkCode, len(inboundTxList))
	return inboundTxList, nil
}

/* GetMasterWallet private methods group */
// @private getNativeBalance Get wallet TON balance
// walletInstance: Wallet instance
// Returns: TON wallet balance
func (ton *Ton) getNativeBalance(walletInstance *wallet.Wallet) (float64, error) {
	ctx := context.Background()

	// Get master chain info
	chainInfo, err := ton.Client.CurrentMasterchainInfo(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get current master chain info: %w", err)
	}

	// Get wallet balance instance
	balance, err := walletInstance.GetBalance(ctx, chainInfo)
	if err != nil {
		return 0, fmt.Errorf("failed to get balance: %w", err)
	}

	// Return balance in TON
	return strconv.ParseFloat(balance.String(), 64)
}

// @private getJettonBalance Get wallet Jetton token balance
// contractAddr: Token contract address
// wallet: Wallet instance
// Returns: Jetton token wallet balance
func (ton *Ton) getJettonBalance(currency, contractAddr, walletAddr string) (float64, error) {
	ctx := context.Background()
	currency = strings.ToUpper(currency)

	// Get master jetton client
	jettonMaster := jetton.NewJettonMasterClient(ton.Client, address.MustParseAddr(contractAddr))

	// Get jetton wallet
	jettonWallet, err := jettonMaster.GetJettonWallet(ctx, address.MustParseAddr(walletAddr))
	if err != nil {
		return 0, fmt.Errorf("failed to get jetton wallet: %w", err)
	}

	// Get jetton balance
	jettonBalance, err := jettonWallet.GetBalance(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get jetton balance: %w", err)
	}

	// Get jetton decimals from config
	decimals := config.TokenSettings[ton.NetworkCode][currency].Decimals
	// Return converted balance
	return strconv.ParseFloat(tlb.MustFromDecimal(jettonBalance.String(), decimals).String(), 64)
}

/* GetTransactionCommission private methods group */
// @private calcNativeFee Calc fee for TON transaction
// walletInstance: Wallet instance
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// Returns: Fee in TON
func (ton *Ton) calcNativeFee(walletInstance *wallet.Wallet, toAddr string, amount float64, memo string) (float64, error) {
	ctx := context.Background()

	// Get converted amount (in nanoTON)
	amountConverted := tlb.MustFromTON(strconv.FormatFloat(amount, 'f', -1, 64))

	// Build message payload (with comment)
	var body *cell.Cell
	if memo != "" {
		var err error
		body, err = wallet.CreateCommentCell(memo)
		if err != nil {
			return 0, fmt.Errorf("failed to create comment cell: %w", err)
		}
	}

	// Build external message
	extMessage, err := walletInstance.BuildExternalMessage(ctx, wallet.SimpleMessage(
		address.MustParseAddr(toAddr), // To address
		amountConverted,               // Amount (in nanoTON)
		body,                          // Message payload
	))
	if err != nil {
		// Fallback - wallet not initialized
		if strings.Contains(err.Error(), "not initialized") {
			return 0.01, nil // ~0.01 TON typical fee
		}
		return 0, fmt.Errorf("failed to build external message: %w", err)
	}

	// Send fee estimation request and return total fee
	return ton.sendCalcFeeRequest(walletInstance.WalletAddress(), extMessage)
}

// @private calcJettonFee Calc fee for Jetton token transaction
// walletInstance: Wallet instance
// currency: Token ticker
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// Returns: Fee in TON
func (ton *Ton) calcJettonFee(walletInstance *wallet.Wallet, currency, toAddr string, amount float64, memo string) (float64, error) {
	ctx := context.Background()
	currency = strings.ToUpper(currency)
	networkCfg, _ := config.GetNetworkConfig(ton.NetworkCode)

	// Get token contract address
	contractAddr, ok := config.GetContractAddress(ton.NetworkCode, currency)
	if !ok {
		return 0, fmt.Errorf("contract address not found: %s/%s", ton.NetworkCode, currency)
	}

	// Get master jetton client
	jettonMaster := jetton.NewJettonMasterClient(ton.Client, address.MustParseAddr(contractAddr))

	// Get jetton wallet
	jettonWallet, err := jettonMaster.GetJettonWallet(ctx, walletInstance.WalletAddress())
	if err != nil {
		// Fallback - wallet not initialized or no jetton wallet
		if strings.Contains(err.Error(), "not initialized") || strings.Contains(err.Error(), "exit code") {
			return networkCfg.FallbackFee, nil // ~0.05 TON for jetton transfer
		}
		return 0, fmt.Errorf("failed to get jetton wallet: %w", err)
	}

	// Get forward fee and message fee from config
	forwardTonAmount := tlb.MustFromTON(networkCfg.ForwardFee)
	messageTonAmount := tlb.MustFromTON(networkCfg.MessageFee)

	// Get jetton decimals from config
	decimals := config.TokenSettings[ton.NetworkCode][currency].Decimals
	// Get converted amount
	amountConverted := tlb.MustFromDecimal(strconv.FormatFloat(amount, 'f', -1, 64), decimals)

	// Build forward payload (with comment)
	var forwardPayload *cell.Cell
	if memo != "" {
		forwardPayload, err = wallet.CreateCommentCell(memo)
		if err != nil {
			return 0, fmt.Errorf("failed to create comment cell: %w", err)
		}
	}

	// Build payload transfer for Jetton
	payload, err := jetton.BuildTransferPayload(
		address.MustParseAddr(toAddr),  // To address
		walletInstance.WalletAddress(), // From address
		amountConverted,                // Jetton amount (in jetton decimals)
		forwardTonAmount,               // Forward TON amount for notification (in nanoTON)
		forwardPayload,                 // Forward payload (comment)
		nil,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to build transfer payload: %w", err)
	}

	// Build external message
	extMessage, err := walletInstance.BuildExternalMessage(ctx, wallet.SimpleMessage(
		jettonWallet.Address(), // Jetton wallet address
		messageTonAmount,       // Message TON amount (in nanoTON)
		payload,                // Transfer payload
	))
	if err != nil {
		// Fallback - wallet not initialized
		if strings.Contains(err.Error(), "not initialized") {
			return networkCfg.FallbackFee, nil // ~0.05 TON for jetton transfer
		}
		return 0, fmt.Errorf("failed to build external message: %w", err)
	}

	// Send fee estimation request and return total fee
	return ton.sendCalcFeeRequest(walletInstance.WalletAddress(), extMessage)
}

// @private sendCalcFeeRequest Send HTTP request and calc total fee
// fromAddr: Wallet address
// extMessage: External message instance
// Returns: Total fee in TON
func (ton *Ton) sendCalcFeeRequest(fromAddr *address.Address, extMessage *tlb.ExternalMessage) (float64, error) {
	// Load config
	cfg, _ := config.Load()

	// Convert message to BOC in base64
	bocBase64 := base64.StdEncoding.EncodeToString(extMessage.Body.ToBOC())

	// Build request body
	reqBody, err := json.Marshal(map[string]any{
		"address":       fromAddr.String(),
		"body":          bocBase64,
		"ignore_chksig": true,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create HTTP request entity with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(
		ctx, "POST", cfg.TonHttpUrl+"/"+cfg.QuickNodeApiKey+"/estimateFee", bytes.NewReader(reqBody),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to build http request: %w", err)
	}

	// Set request headers
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")

	// Send HTTP request to RPC provider
	response, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("failed to send estimate fee request: %w", err)
	}
	defer response.Body.Close()

	// Check HTTP status
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("estimate fee request failed with status: %d", response.StatusCode)
	}

	// Read and parse response body
	var respData struct {
		Result struct {
			SourceFees struct {
				InFwdFee   int `json:"in_fwd_fee"`
				StorageFee int `json:"storage_fee"`
				GasFee     int `json:"gas_fee"`
				FwdFee     int `json:"fwd_fee"`
			} `json:"source_fees"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&respData); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}

	// Calc total fee
	fees := respData.Result.SourceFees
	totalNano := fees.InFwdFee + fees.StorageFee + fees.GasFee + fees.FwdFee

	total := tlb.FromNanoTON(big.NewInt(int64(totalNano))).String()
	totalFloat, err := strconv.ParseFloat(total, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse total fee: %w", err)
	}

	return totalFloat, nil
}

/* SendTransaction private methods group */
// @private sendNative Send native TON transaction
// walletInstance: Wallet instance
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (ton *Ton) sendNative(walletInstance *wallet.Wallet, toAddr string, amount float64, memo string) (string, error) {
	ctx := context.Background()

	// Get converted amount (in nanoTON)
	amountConverted := tlb.MustFromTON(strconv.FormatFloat(amount, 'f', -1, 64))

	// Build message payload (with comment)
	var body *cell.Cell
	if memo != "" {
		var err error
		body, err = wallet.CreateCommentCell(memo)
		if err != nil {
			return "", fmt.Errorf("failed to create comment cell: %w", err)
		}
	}

	// Send transaction and wait for confirmation
	tx, _, err := walletInstance.SendWaitTransaction(ctx, wallet.SimpleMessage(
		address.MustParseAddr(toAddr), // To address
		amountConverted,               // Amount (in nanoTON)
		body,                          // Message payload (comment)
	))
	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	// Return transaction hash
	return base64.StdEncoding.EncodeToString(tx.Hash), nil
}

// @private sendJetton Send Jetton token transaction
// currency: Token ticker
// walletInstance: Wallet instance
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (ton *Ton) sendJetton(currency string, walletInstance *wallet.Wallet, toAddr string, amount float64, memo string) (string, error) {
	ctx := context.Background()
	currency = strings.ToUpper(currency)
	networkCfg, _ := config.GetNetworkConfig(ton.NetworkCode)

	// Get token contract address from config
	contractAddr, ok := config.GetContractAddress(ton.NetworkCode, currency)
	if !ok {
		return "", fmt.Errorf("contract address not found: %s/%s", ton.NetworkCode, currency)
	}

	// Get jetton master client
	jettonMaster := jetton.NewJettonMasterClient(ton.Client, address.MustParseAddr(contractAddr))

	// Get jetton wallet
	jettonWallet, err := jettonMaster.GetJettonWallet(ctx, walletInstance.WalletAddress())
	if err != nil {
		return "", fmt.Errorf("failed to get sender jetton wallet: %w", err)
	}

	// Get forward fee and message fee from config
	forwardTonAmount := tlb.MustFromTON(networkCfg.ForwardFee)
	messageTonAmount := tlb.MustFromTON(networkCfg.MessageFee)

	// Get jetton decimals from config
	decimals := config.TokenSettings[ton.NetworkCode][currency].Decimals
	// Get converted amount
	amountConverted := tlb.MustFromDecimal(strconv.FormatFloat(amount, 'f', -1, 64), decimals)

	// Build forward payload (with comment)
	var forwardPayload *cell.Cell
	if memo != "" {
		forwardPayload, err = wallet.CreateCommentCell(memo)
		if err != nil {
			return "", fmt.Errorf("failed to create comment cell: %w", err)
		}
	}

	// Build transfer payload
	transferPayload, err := jetton.BuildTransferPayload(
		address.MustParseAddr(toAddr),  // To address
		walletInstance.WalletAddress(), // From address
		amountConverted,                // Amount (in jetton decimals)
		forwardTonAmount,               // Forward TON amount for notification (in nanoTON)
		forwardPayload,                 // Forward payload (comment)
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("failed to build transfer payload: %w", err)
	}

	// Send transaction and wait for confirmation
	tx, _, err := walletInstance.SendWaitTransaction(ctx, wallet.SimpleMessage(
		jettonWallet.Address(), // Jetton wallet address
		messageTonAmount,       // Message TON amount (in nanoTON)
		transferPayload,        // Transfer payload
	))
	if err != nil {
		return "", fmt.Errorf("failed to send jetton transaction: %w", err)
	}

	// Return transaction hash
	return base64.StdEncoding.EncodeToString(tx.Hash), nil
}
