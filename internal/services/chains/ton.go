package chains

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"

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
)

type (
	Ton struct {
		BaseChainService
		Client        ton.APIClientWrapped
		WalletVersion wallet.Version
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
	// Create new Ton ADNL API client
	apiClient := ton.NewAPIClient(liteclient.NewConnectionPool()).WithRetry()

	networkCode := "ton"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	return &Ton{
		BaseChainService: BaseChainService{
			UserRepo:    repository.NewUserRepository(),
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
		},
		Client:        apiClient,
		WalletVersion: wallet.V4R2,
	}
}

// GetCode Get network code
func (ton *Ton) GetCode() string {
	return ton.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
// userId: Unique user id
// Returns: Master wallet address (for using MEMO tags)
func (ton *Ton) GetUserWallet(userId int) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + ton.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	return masterData["address"].(string), nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of mster wallet info (address and balances of all available tokens)
func (ton *Ton) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + ton.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)
	seedWords := strings.Split(masterData["mnemonic"].(string), "")

	// Init wallet instance
	walletInstance, err := wallet.FromSeedWithOptions(ton.Client, seedWords, ton.WalletVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to init wallet instance: %w", err)
	}

	// Get Jetton tokens balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[ton.NetworkCode].(map[string]string) {
		tokenBalance, _ := ton.getJettonBalance(token, contractAddr, masterAddr)
		balances[token] = tokenBalance
	}

	// Get TON native balance
	tonBalance, _ := ton.getNativeBalance(walletInstance)
	balances[ton.NativeToken] = tonBalance

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
func (ton *Ton) GetTransactionCommission(
	oType, wType string, wId *string, currency, toAddr string, amount float64, memo string,
) (map[string]float64, error) {
	var walletPath string
	if wType == "master" {
		walletPath = "/master/" + ton.NetworkCode
	} else {
		if wId == nil {
			return nil, fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, ton.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet mnemonic
	mnemonic, ok := walletData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return nil, fmt.Errorf("invalid or missing %s wallet mnemonic in Vault", ton.NetworkCode)
	}
	seedWords := strings.Split(mnemonic, "")

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
		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetHTTPClient().GetWithdrawalFee(currency, ton.NetworkCode)

		// Get AML check cost from config (in USD value)
		cfg, _ := config.Load()
		amlFee, _ = strconv.ParseFloat(cfg.AmlCheckCost, 64)
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
// Returns: Transaction status (always success)
func (ton *Ton) GetTransactionStatus(hash, toAddr string) (string, error) {
	if toAddr == "" {
		return "", fmt.Errorf("to address is required")
	}

	ctx := context.Background()
	to := address.MustParseAddr(toAddr)

	// Get master chain info
	chainInfo, err := ton.Client.CurrentMasterchainInfo(ctx)
	if err != nil {
		return "", err
	}
	// Get account info
	account, err := ton.Client.GetAccount(ctx, chainInfo, to)
	if err != nil {
		return "", err
	}

	// Get last LT and tx hash from account as global batch params
	lt := account.LastTxLT
	txHash := account.LastTxHash

	for {
		// Get transaction batch for current batch params
		txs, err := ton.Client.ListTransactions(ctx, to, 20, lt, txHash)
		if err != nil {
			return "", err
		}
		if len(txs) == 0 {
			return "failed", nil
		}

		for _, tx := range txs {
			if hex.EncodeToString(tx.Hash) == hash {
				return "success", nil
			}
		}

		// Set latest LT and tx hash to batch params
		// txs[0] - the oldest value in batch
		oldest := txs[0]
		lt, txHash = oldest.LT, oldest.Hash
	}
}

// SendTransaction Send transaction to the network
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (ton *Ton) SendTransaction(wType string, wId *string, currency, toAddr string, amount float64, memo string) (string, error) {
	var walletPath string
	if wType == "master" {
		walletPath = fmt.Sprintf("/master/%s", ton.NetworkCode)
	} else {
		if wId == nil {
			return "", fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, ton.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return "", fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet mnemonic
	mnemonic, okM := walletData["mnemonic"].(string)
	if !okM {
		return "", fmt.Errorf("invalid or missing %s wallet data in Vault", ton.NetworkCode)
	}

	// Init wallet instance
	seedWords := strings.Split(mnemonic, "")
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
func (ton *Ton) GetInboundTransactions() ([]*InboundTransaction, error) {
	ctx := context.Background()

	// Get last logical time from Redis
	lastLTStr, err := redis.GetClient().HGet(ctx, "last_blocks", ton.NetworkCode)
	var lastLT uint64
	if err != nil {
		lastLT = 0
	} else {
		lastLT, _ = strconv.ParseUint(lastLTStr, 10, 64)
	}

	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + ton.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get master data: %w", ton.NetworkCode, err)
	}
	masterAddrStr := masterData["address"].(string)
	// Parse master address
	masterAddr := address.MustParseAddr(masterAddrStr)

	// Get TON decimals from config
	tonDecimals, err := config.GetChainSetting[int64](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", ton.NetworkCode, "TON"),
	)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get %s decimals from config: %w", ton.NetworkCode, "ton", err)
	}

	// Cache for jetton info
	jettonCache := make(map[string]*JettonInfo)
	var jettonCacheMu sync.RWMutex

	// Get transactions list (up to 100 transactions)
	txList, err := ton.Client.ListTransactions(ctx, masterAddr, 100, lastLT, nil)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get transactions for master address: %w",
			ton.NetworkCode, err)
	}

	log.Printf("[%s] Processing %d transactions for master address from LT %d",
		ton.NetworkCode, len(txList), lastLT)

	// Processed inbound transactions result list
	inboundTxList := make([]*InboundTransaction, 0)
	// Set max LT to last LT value
	maxLT := lastLT

	// Process transactions
	for _, tx := range txList {
		// Update max LT
		if tx.LT > maxLT {
			maxLT = tx.LT
		}

		// Only process incoming messages
		if tx.IO.In == nil || tx.IO.In.MsgType != tlb.MsgTypeInternal {
			continue
		}

		// Safely convert message to InternalMessage
		intMsg := tx.IO.In.AsInternal()
		// Get transaction hash
		txHash := fmt.Sprintf("%x", tx.Hash)

		// Extract comment from message body
		var comment string
		if intMsg.Body != nil {
			slice := intMsg.Body.BeginParse()
			// Check if we have at least 32 bits for op code
			if slice.BitsLeft() >= 32 {
				op, err := slice.LoadUInt(32)
				if err == nil && op == 0 {
					text, err := slice.LoadStringSnake()
					if err == nil {
						comment = strings.TrimSpace(text)
					}
				}
			}
		}
		// Skip if no comment found
		if comment == "" {
			log.Printf("[%s] Transaction %x has no comment, skipping", ton.NetworkCode, txHash)
			continue
		}

		// Get user id by comment (memo)
		userId, err := ton.UserRepo.GetUserIdByNumber(comment)
		if err != nil {
			log.Printf("[%s] Failed to get user id by memo '%s' for tx %x: %v",
				ton.NetworkCode, comment, txHash, err)
			continue
		}

		// Get from address
		fromAddr := intMsg.SrcAddr.String()

		// Check if it's a native TON transfer
		if intMsg.Amount.Nano().Uint64() > 0 {
			// Get converted amount (in TON)
			amount := utils.RemoveDecimals(intMsg.Amount.Nano(), tonDecimals)

			inboundTxList = append(inboundTxList, &InboundTransaction{
				UserId:   int(userId),
				Currency: "TON",
				TxId:     txHash,
				From:     fromAddr,
				To:       masterAddr.String(),
				Amount:   amount,
			})

			log.Printf("[%s] TON transaction '%s' processed successful", ton.NetworkCode, txHash)
		}

		// Check if it's a Jetton transfer
		if intMsg.Body != nil {
			// Parse jetton transfer from message
			slice := intMsg.Body.BeginParse()

			// Check if we have enough bits for op code
			if slice.BitsLeft() < 32 {
				continue
			}
			// Get op code
			opCode, err := slice.LoadUInt(32)
			if err != nil {
				continue
			}
			// 0x7362d09c - internal_transfer (jetton transfer to wallet)
			if opCode != 0x7362d09c {
				continue
			}

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

			// Read from address (sender of jettons)
			jettonSender, err := slice.LoadAddr()
			if err != nil {
				continue
			}

			// Get jetton wallet address (message sender is jetton wallet)
			jettonWalletAddr := intMsg.SrcAddr.String()

			// Get jetton info from cache or fetch from contract
			jettonCacheMu.RLock()
			cached, exists := jettonCache[jettonWalletAddr]
			jettonCacheMu.RUnlock()

			var jettonInfo *JettonInfo
			if exists {
				// Get jetton info from cache
				jettonInfo = cached
			} else {
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
				token, err := config.GetTokenByContractAddr(ton.NetworkCode, jettonMasterAddr.String())
				if err != nil {
					log.Printf("[%s] Unknown jetton master address '%s'", ton.NetworkCode, jettonMasterAddr.String())
					continue
				}

				// Get decimals from config
				decimals, err := config.GetChainSetting[int64](
					config.TokenSettings,
					fmt.Sprintf("%s.%s.decimals", ton.NetworkCode, token),
				)
				if err != nil {
					log.Printf("[%s] Failed to get %s decimals: %v", ton.NetworkCode, token, err)
					continue
				}

				jettonInfo = &JettonInfo{
					masterAddr: jettonMasterAddr,
					decimals:   decimals,
					token:      token,
				}

				// Save jetton info to cache
				jettonCacheMu.Lock()
				jettonCache[jettonWalletAddr] = jettonInfo
				jettonCacheMu.Unlock()
			}

			// Get converted amount (with removed decimals)
			amount := utils.RemoveDecimals(jettonAmount, jettonInfo.decimals)

			inboundTxList = append(inboundTxList, &InboundTransaction{
				UserId:   int(userId),
				Currency: jettonInfo.token,
				TxId:     txHash,
				From:     jettonSender.String(),
				To:       masterAddr.String(),
				Amount:   amount,
			})

			log.Printf("[%s] Jetton %s transaction '%s' processed successful",
				ton.NetworkCode, jettonInfo.token, txHash)
		}
	}

	// Save max LT as last processed
	if maxLT > lastLT {
		err = redis.GetClient().HSet(ctx, "last_blocks", ton.NetworkCode, maxLT)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save last LT in Redis: %w", ton.NetworkCode, err)
		}
		log.Printf("[%s] Saved last LT: %d", ton.NetworkCode, maxLT)
	}

	log.Printf("[%s] Processed %d transactions successful", ton.NetworkCode, len(inboundTxList))

	return inboundTxList, nil
}

/* GetUserWallet private methods group */
// @private deriveAddress Derive HD-wallet address by derivation index from master wallet private key
// masterPrivateKey: Master wallet private key
// userId: Unique user id for derivation index
// Returns: Derived address
func (ton *Ton) deriveAddress(masterPrivateKey string, userId uint32) (string, error) {
	// Get seed bytes from private key
	seed, err := hex.DecodeString(masterPrivateKey)
	if err != nil {
		return "", fmt.Errorf("invalid master key format: %w", err)
	}

	// Derive private key
	privateKey, _ := ton.deriveEd25519Key(seed, userId)

	// Create new wallet instance from private key
	w, err := wallet.FromPrivateKey(nil, privateKey, ton.WalletVersion)
	if err != nil {
		return "", fmt.Errorf("failed to create wallet: %w", err)
	}

	// Get address
	return w.WalletAddress().String(), nil
}

// @private deriveEd25519Key Ton Ed25519 keys derivation
// seed: Seed bytes from private key
// userId: Unique user id for derivation index
// Returns: Address in Ton Ed25519 format
func (ton *Ton) deriveEd25519Key(seed []byte, userId uint32) (ed25519.PrivateKey, ed25519.PublicKey) {
	// Init master HD key for BIP32-Ed25519
	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	mac.Write(seed)
	result := mac.Sum(nil)

	key := result[:32]
	chainCode := result[32:]

	// Ton uses derivation scheme based on HMAC-SHA512
	// Derivation path: m/44'/607'/0'/0'/{userId}'
	path := []uint32{
		0x8000002C,          // 44'
		0x8000025F,          // 607' (TON coin type)
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
	decimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", ton.NetworkCode, strings.ToUpper(currency)),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get %s decimals: %w", currency, err)
	}

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
	extMessage, err := walletInstance.BuildExternalMessage(
		ctx,
		wallet.SimpleMessage(
			address.MustParseAddr(toAddr), // To address
			amountConverted,               // Amount (in nanoTON)
			body,                          // Message payload
		),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to build external message: %w", err)
	}

	// Return total fee
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

	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", ton.NetworkCode, currency),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get master jetton client
	jettonMaster := jetton.NewJettonMasterClient(ton.Client, address.MustParseAddr(contractAddr))

	// Get jetton wallet
	jettonWallet, err := jettonMaster.GetJettonWallet(ctx, walletInstance.WalletAddress())
	if err != nil {
		return 0, fmt.Errorf("failed to get jetton wallet: %w", err)
	}

	// Get forward fee and message fee from config
	forwardTon, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.forward_fee", ton.NetworkCode),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get forward fee amount: %w", err)
	}
	messageTon, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.message_fee", ton.NetworkCode),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get message fee amount: %w", err)
	}
	forwardTonAmount := tlb.MustFromTON(forwardTon)
	messageTonAmount := tlb.MustFromTON(messageTon)

	// Get jetton decimals from config
	decimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", ton.NetworkCode, strings.ToUpper(currency)),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get %s decimals: %w", currency, err)
	}
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
	extMessage, err := walletInstance.BuildExternalMessage(
		ctx,
		wallet.SimpleMessage(
			jettonWallet.Address(), // Jetton wallet address
			messageTonAmount,       // Message TON amount (in nanoTON)
			payload,                // Transfer payload
		),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to build external message: %w", err)
	}

	// Return total fee
	return ton.sendCalcFeeRequest(walletInstance.WalletAddress(), extMessage)
}

// @private sendCalcFeeRequest Send HTTP request and calc total fee
// fromAddr: Wallet address
// extMessage: External message instance
// Returns: Total fee in TON
func (ton *Ton) sendCalcFeeRequest(fromAddr *address.Address, extMessage *tlb.ExternalMessage) (float64, error) {
	// Convert message to BOC in base64
	bocBase64 := base64.StdEncoding.EncodeToString(extMessage.Body.ToBOC())
	// Set params for request
	reqBody, _ := json.Marshal(map[string]any{
		"address":       fromAddr,
		"body":          bocBase64,
		"ignore_chksig": true,
	})

	// Build HTTP request
	cfg, _ := config.Load()
	httpReq, err := http.NewRequest("POST", cfg.TonRpcUrl+"/estimateFee", bytes.NewReader(reqBody))
	if err != nil {
		return 0, fmt.Errorf("failed to build http request: %w", err)
	}

	// Set additional headers
	httpReq.Header.Set("accept", "application/json")
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", cfg.TatumApiKey)

	// Send estimateFee HTTP request to RPC provider
	response, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("failed to get estimate fee from RPC provider: %w", err)
	}
	defer response.Body.Close()

	respBody, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %w", err)
	}

	var respData struct {
		Result struct {
			SourceFees struct {
				InFwdFee   int `json:"in_fwd_fee"`
				StorageFee int `json:"storage_fee"`
				GasFee     int `json:"gas_fee"`
				FwdFee     int `json:"fwd_fee"`
			} `json:"source_fees"`
			DestinationFees any `json:"destination_fees"`
		} `json:"result"`
	}
	err = json.Unmarshal(respBody, &respData)
	if err != nil {
		return 0, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	// Calc total fee
	total := big.NewInt(int64(respData.Result.SourceFees.InFwdFee + respData.Result.SourceFees.StorageFee +
		respData.Result.SourceFees.GasFee + respData.Result.SourceFees.FwdFee))

	// Get TON decimals from config
	tonDecimals, _ := config.GetChainSetting[int64](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", ton.NetworkCode, "TON"),
	)
	// Return converted total fee
	return utils.RemoveDecimals(total, tonDecimals), nil
}

/* SendTransaction private methods group */
// @private sendNative Send native TON transaction
// walletInstance: Wallet instance
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// privateKey: Wallet private key
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
	tx, _, err := walletInstance.SendWaitTransaction(
		ctx,
		wallet.SimpleMessage(
			address.MustParseAddr(toAddr), // To address
			amountConverted,               // Amount (in nanoTON)
			body,                          // Message payload (comment)
		),
	)
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
// privateKey: Wallet private key
// Returns: Transaction hash
func (ton *Ton) sendJetton(currency string, walletInstance *wallet.Wallet, toAddr string, amount float64, memo string) (string, error) {
	ctx := context.Background()

	// Get token contract address from config
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", ton.NetworkCode, currency),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get jetton master client
	jettonMaster := jetton.NewJettonMasterClient(ton.Client, address.MustParseAddr(contractAddr))

	// Get jetton wallet
	jettonWallet, err := jettonMaster.GetJettonWallet(ctx, walletInstance.WalletAddress())
	if err != nil {
		return "", fmt.Errorf("failed to get sender jetton wallet: %w", err)
	}

	// Get jetton decimals from config
	decimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", ton.NetworkCode, strings.ToUpper(currency)),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get %s decimals: %w", currency, err)
	}
	// Get converted amount
	amountConverted := tlb.MustFromDecimal(strconv.FormatFloat(amount, 'f', -1, 64), decimals)

	// Get forward fee and message fee from config
	forwardTon, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.forward_fee", ton.NetworkCode),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get forward fee amount: %w", err)
	}
	messageTon, err := config.GetChainSetting[string](
		config.NetworkSettings,
		fmt.Sprintf("%s.message_fee", ton.NetworkCode),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get message fee amount: %w", err)
	}
	forwardTonAmount := tlb.MustFromTON(forwardTon)
	messageTonAmount := tlb.MustFromTON(messageTon)

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

	// Create message to jetton wallet contract
	msg := wallet.SimpleMessage(
		jettonWallet.Address(), // Jetton wallet address
		messageTonAmount,       // Message TON amount (in nanoTON)
		transferPayload,        // Transfer payload
	)

	// Send transaction and wait for confirmation
	tx, _, err := walletInstance.SendWaitTransaction(ctx, msg)
	if err != nil {
		return "", fmt.Errorf("failed to send jetton transaction: %w", err)
	}

	// Return transaction hash
	return base64.StdEncoding.EncodeToString(tx.Hash), nil
}
