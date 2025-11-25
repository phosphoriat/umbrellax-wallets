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
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"wallets/internal/config"
	"wallets/internal/database/vault"
	"wallets/internal/services/api/aml"
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

type Ton struct {
	BaseChainService
	Client        ton.APIClientWrapped
	WalletVersion wallet.Version
}

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
//
//	Returns: Master wallet address (for using MEMO tags)
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
	walletInstance, err := wallet.FromSeed(ton.Client, seedWords, ton.WalletVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to init wallet instance: %w", err)
	}

	// Get Jetton tokens balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[ton.NetworkCode].(map[string]string) {
		tokenBalance, _ := ton.getJettonBalance(contractAddr, masterAddr)
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
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag
// Returns: Map of all fee types with their values (chain_fee, bybit_fee, aml_fee)
func (ton *Ton) GetTransactionCommission(currency, toAddr string, amount float64, memo string) (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + ton.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	seedWords := strings.Split(masterData["mnemonic"].(string), "")

	// Init master wallet instance
	walletInstance, err := wallet.FromSeed(ton.Client, seedWords, ton.WalletVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to init wallet instance: %w", err)
	}

	// Calc chain fee for currency
	var chainFee float64 = 0
	switch currency {
	case "TON":
		chainFee, _ = ton.calcNativeFee(walletInstance, toAddr, amount, memo)

	default:
		chainFee, _ = ton.calcJettonFee(walletInstance, currency, toAddr, amount, memo)
	}

	// Get bybit withdrawal fee
	bybitFee, _ := bybit.GetHTTPClient().GetWithdrawalFee(currency, ton.NetworkCode)

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
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (ton *Ton) SendTransaction(currency, toAddr string, amount float64, memo string) (string, string, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + ton.NetworkCode)
	if err != nil {
		return "", "", fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return "", "", fmt.Errorf("invalid or missing %s master address in Vault", ton.NetworkCode)
	}

	// AML check to address
	amlId, riskLevel, err := aml.GetClient().CheckAddress(ton.NetworkCode, toAddr)
	if err != nil {
		return "", "", fmt.Errorf("AML check address '%s' at network '%s' failed: %w", toAddr, ton.NetworkCode, err)
	}
	// If high or severe AML risks detected, return error without sending transaction
	if riskLevel == "high" || riskLevel == "severe" {
		return "", amlId, fmt.Errorf("failed to send withdrawal transaction to '%s' in '%s': %s AML risk", toAddr, ton.NetworkCode, riskLevel)
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "TON":
		hash, err = ton.sendNative(masterAddr, toAddr, amount, memo)
	default:
		hash, err = ton.sendJetton(currency, masterAddr, toAddr, amount, memo)
	}

	if err != nil {
		return "", "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, amlId, nil
}

// GetInboundTransactions Get new inbound transactions list
func (ton *Ton) GetInboundTransactions() ([]map[string]any, error) {

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
// wallet: Wallet instance
// Returns: TON wallet balance
func (ton *Ton) getNativeBalance(wallet *wallet.Wallet) (float64, error) {
	// Get master chain info
	chainInfo, err := ton.Client.CurrentMasterchainInfo(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get current master chain info: %w", err)
	}

	// Get wallet balance instance
	balance, err := wallet.GetBalance(context.Background(), chainInfo)
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
func (ton *Ton) getJettonBalance(contractAddr, walletAddr string) (float64, error) {
	// Get master jetton client
	jettonMaster := jetton.NewJettonMasterClient(ton.Client, address.MustParseAddr(contractAddr))

	// Get jetton wallet
	jettonWallet, err := jettonMaster.GetJettonWallet(context.Background(), address.MustParseAddr(walletAddr))
	if err != nil {
		return 0, fmt.Errorf("failed to get jetton wallet: %w", err)
	}

	// Get jetton balance
	jettonBalance, err := jettonWallet.GetBalance(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get jetton balance: %w", err)
	}

	// Return converted balance
	return strconv.ParseFloat(jettonBalance.String(), 64)
}

/* GetTransactionCommission private methods group */
// @private calcNativeFee Calc fee for TON transaction
// walletInstance: Master wallet instance
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// Returns: Fee in TON
func (ton *Ton) calcNativeFee(walletInstance *wallet.Wallet, toAddr string, amount float64, memo string) (float64, error) {
	// Build external message
	extMessage, err := walletInstance.BuildExternalMessage(
		context.Background(),
		wallet.SimpleMessage(
			address.MustParseAddr(toAddr),                             // To address
			tlb.MustFromTON(strconv.FormatFloat(amount, 'f', -1, 64)), // TON amount
			cell.BeginCell().
				MustStoreUInt(0, 32).
				MustStoreStringSnake(memo).
				EndCell(), // Payload
		),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to build external message: %w", err)
	}

	// Return total fee
	return ton.sendCalcFeeRequest(walletInstance.WalletAddress(), extMessage)
}

// @private calcJettonFee Calc fee for Jetton token transaction
// walletInstance: Master wallet instance
// currency: Token ticker
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// Returns: Fee in TON
func (ton *Ton) calcJettonFee(walletInstance *wallet.Wallet, currency, toAddr string, amount float64, memo string) (float64, error) {
	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](config.ContractsAddresses, fmt.Sprintf("%s.%s", ton.NetworkCode, currency))
	if err != nil {
		return 0, fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get master jetton client
	jettonMaster := jetton.NewJettonMasterClient(ton.Client, address.MustParseAddr(contractAddr))

	// Get jetton wallet
	jettonWallet, err := jettonMaster.GetJettonWallet(context.Background(), walletInstance.WalletAddress())
	if err != nil {
		return 0, fmt.Errorf("failed to get jetton wallet: %w", err)
	}

	// Set forwardTON and messageTON
	forwardTon := tlb.MustFromTON("0.02")
	messageTon := tlb.MustFromTON("0.1")

	// Build payload transfer for Jetton
	payload, err := jetton.BuildTransferPayload(
		address.MustParseAddr(toAddr),                             // To address
		walletInstance.WalletAddress(),                            // From address (Response destination)
		tlb.MustFromTON(strconv.FormatFloat(amount, 'f', -1, 64)), // Jetton amount
		forwardTon, // Forward TON amount
		nil,
		nil,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to build transfer payload: %w", err)
	}

	// Build external message
	extMessage, err := walletInstance.BuildExternalMessage(
		context.Background(),
		wallet.SimpleMessage(
			jettonWallet.Address(),
			messageTon,
			payload,
		),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to build external message: %w", err)
	}

	// Return total fee
	return ton.sendCalcFeeRequest(walletInstance.WalletAddress(), extMessage)
}

// @private sendCalcFeeRequest Send HTTP request and calc total fee
// walletInstance: Master wallet instance
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
	tonDecimals, _ := config.GetChainSetting[int64](config.TokenSettings, fmt.Sprintf("%s.ton.decimals", ton.NetworkCode))
	// Return converted total fee
	return utils.RemoveDecimals(total, tonDecimals), nil
}

/* SendTransaction private methods group */
// @private sendNative Send native TON transaction
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (ton *Ton) sendNative(fromAddr, toAddr string, amount float64, memo string) (string, error) {

}

// @private sendJetton Send Jetton token transaction
// currency: Token ticker
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (ton *Ton) sendJetton(currency, fromAddr, toAddr string, amount float64, memo string) (string, error) {

}

// @private signAndBroadcast Sign and broadcast transaction to the network
// tx: Transaction struct
// Returns: Transaction hash
func (ton *Ton) signAndBroadcast(tx) (string, error) {

}
