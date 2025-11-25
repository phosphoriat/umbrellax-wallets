package chains

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"

	"wallets/internal/config"
	"wallets/internal/database/vault"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

type Solana struct {
	BaseChainService
	rpcClient *rpc.Client
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
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
		},
		rpcClient: client,
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

	// Get SPL tokens balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[sol.NetworkCode].(map[string]string) {
		tokenBalance, _ := sol.getSplBalance(contractAddr, masterAddr)
		balances[token] = tokenBalance
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
func (sol *Solana) GetTransactionCommission(currency, to string, amount float64, memo string) (map[string]any, error) {

}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status
func (sol *Solana) GetTransactionStatus(hash, toAddr string) (string, error) {

}

// SendTransaction Send transaction to the network
func (sol *Solana) SendTransaction(currency, to string, amount float64, memo string) (string, string, error) {

}

// GetInboundTransactions Get new inbound transactions list
func (sol *Solana) GetInboundTransactions() ([]map[string]any, error) {

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

}

// @private getSplBalance Get wallet SPL token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: SPL token wallet balance
func (sol *Solana) getSplBalance(contractAddr, addr string) (float64, error) {

}

/* GetTransactionCommission private methods group */
// @private calcNativeFee Calc fee for SOL transaction
// fromAddress: Master wallet address
// toAddress: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in SOL
func (sol *Solana) calcNativeFee(fromAddr, toAddr string, amount float64) (float64, error) {

}

// @private calcSplFee Calc fee for SPL token transaction
// currency: Token ticker
// fromAddress: Master wallet address
// toAddress: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in SOL
func (sol *Solana) calcSplFee(currency, fromAddr, toAddr string, amount float64) (float64, error) {

}

/* SendTransaction private methods group */
// @private sendNative Send native SOL transaction
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Transaction hash
func (sol *Solana) sendNative(fromAddr, toAddr string, amount float64) (string, error) {

}

// @private sendSpl Send SPL token transaction
// currency: Token ticker
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Transaction hash
func (sol *Solana) sendSpl(currency, fromAddr, toAddr string, amount float64) (string, error) {

}

// @private signAndBroadcast Sign and broadcast transaction to the network
// tx: Transaction struct
// Returns: Transaction hash
func (sol *Solana) signAndBroadcast(tx) (string, error) {

}
