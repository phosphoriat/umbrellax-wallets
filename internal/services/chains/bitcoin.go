package chains

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"sync"

	"wallets/internal/config"
	"wallets/internal/database/vault"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/rpcclient"
)

type Bitcoin struct {
	BaseChainService
	Client *rpcclient.Client
}

// Global package vars
var (
	btcInstance *Bitcoin
	btcOnce     sync.Once
)

// GetBitcoin Get Bitcoin chain instance
func GetBitcoin() *Bitcoin {
	btcOnce.Do(func() {
		btcInstance = newBitcoin()
	})
	return btcInstance
}

// @private newBitcoin Create new instance of Tron (private constructor)
func newBitcoin() *Bitcoin {
	// Load config
	cfg, _ := config.Load()

	// Create RPC client with default options
	client, err := rpcclient.New(&rpcclient.ConnConfig{
		Host:         cfg.BtcRpcUrl,
		HTTPPostMode: true,
		DisableTLS:   false,
		ExtraHeaders: map[string]string{
			"x-api-key":    cfg.TatumApiKey,
			"Content-Type": "application/json",
		},
	}, nil)
	if err != nil {
		log.Printf("failed to connect Bitcoin RPC API: %v", err)
	}

	networkCode := "bitcoin"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	return &Bitcoin{
		BaseChainService: BaseChainService{
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
		},
		Client: client,
	}
}

// GetCode Get network code
func (btc *Bitcoin) GetCode() string {
	return btc.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
// userId: Unique user id
func (btc *Bitcoin) GetUserWallet(userId int) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + btc.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	// Derive user address from master private key
	derivedAddr, err := btc.deriveAddress(masterData["privateKey"].(string), uint32(userId))
	if err != nil {
		return "", err
	}

	return derivedAddr, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
func (btc *Bitcoin) GetMasterWallet() (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + btc.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Get BTC balance
	btcBalance, _ := 0

	return map[string]any{
		"address": masterAddr,
		"balance": btcBalance,
	}, nil
}

// GetTransactionCommission Get transaction commission
func (btc *Bitcoin) GetTransactionCommission(currency, to string, amount float64, memo string) (map[string]any, error) {

}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status
func (btc *Bitcoin) GetTransactionStatus(hash, toAddr string) (string, error) {

}

// SendTransaction Send transaction to the network
func (btc *Bitcoin) SendTransaction(currency, to string, amount float64, memo string) (string, string, error) {

}

// GetInboundTransactions Get new inbound transactions list
func (btc *Bitcoin) GetInboundTransactions() ([]map[string]any, error) {

}

/* GetUserWallet private methods group */
// @private deriveAddress Derive HD-wallet address by derivation index from master wallet private key
// masterPrivateKey: Master wallet private key
// userId: Unique user id for derivation index
func (btc *Bitcoin) deriveAddress(masterPrivateKey string, userId uint32) (string, error) {
	// Get seed bytes from private key
	seed, err := hex.DecodeString(masterPrivateKey)
	if err != nil {
		return "", fmt.Errorf("invalid master key format: %w", err)
	}

	// Derive private key using BIP84 for Native SegWit
	_, publicKey, err := btc.deriveSecp256k1Key(seed, userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive key: %w", err)
	}

	// Create P2WPKH address (Native SegWit - bc1...)
	witnessProg := btcutil.Hash160(publicKey.SerializeCompressed())
	addr, err := btcutil.NewAddressWitnessPubKeyHash(witnessProg, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create segwit address: %w", err)
	}

	return addr.EncodeAddress(), nil
}

// @private deriveSecp256k1Key Bitcoin secp256k1 keys derivation for Native SegWit (BIP84)
func (btc *Bitcoin) deriveSecp256k1Key(seed []byte, userId uint32) (*btcec.PrivateKey, *btcec.PublicKey, error) {
	// Init master HD key for BIP32
	mac := hmac.New(sha512.New, []byte("Bitcoin seed"))
	mac.Write(seed)
	result := mac.Sum(nil)

	key := result[:32]
	chainCode := result[32:]

	// BIP84 derivation path for Native SegWit: m/84'/0'/0'/0/{userId}
	path := []uint32{
		0x80000054, // 84' (BIP84 for Native SegWit)
		0x80000000, // 0' for mainnet, 1' for testnet
		0x80000000, // 0' (account)
		0,          // 0 (external chain, non-hardened)
		userId,     // {userId} (address index, non-hardened)
	}

	// Apply HMAC-SHA512 for each path level
	currentKey := key
	currentChainCode := chainCode

	for i, index := range path {
		var data []byte

		if index >= 0x80000000 { // Hardened derivation
			// data = 0x00 || private_key || index
			data = make([]byte, 37)
			data[0] = 0x00
			copy(data[1:33], currentKey)
			binary.BigEndian.PutUint32(data[33:], index)
		} else { // Non-hardened derivation
			// data = public_key || index
			privKey, _ := btcec.PrivKeyFromBytes(currentKey)
			pubKey := privKey.PubKey()
			data = make([]byte, 37)
			copy(data[:33], pubKey.SerializeCompressed())
			binary.BigEndian.PutUint32(data[33:], index)
		}

		hmacHash := hmac.New(sha512.New, currentChainCode)
		hmacHash.Write(data)
		result := hmacHash.Sum(nil)

		// Parse the new key
		newKey := result[:32]
		newChainCode := result[32:]

		// For secp256k1, need to add the parent key to the child key (modulo n)
		if i > 0 { // Skip for master key
			parentKey, _ := btcec.PrivKeyFromBytes(currentKey)
			childKeyScalar := new(btcec.ModNScalar)
			childKeyScalar.SetByteSlice(newKey)

			parentScalar := new(btcec.ModNScalar)
			parentScalar.Set(&parentKey.Key)

			childKeyScalar.Add(parentScalar)

			// Convert [32]byte array to []byte slice
			keyBytes := childKeyScalar.Bytes()
			newKey = keyBytes[:]
		}

		currentKey = newKey
		currentChainCode = newChainCode
	}

	// Create final private key
	privateKey, _ := btcec.PrivKeyFromBytes(currentKey)
	if privateKey == nil {
		return nil, nil, fmt.Errorf("failed to create private key")
	}

	publicKey := privateKey.PubKey()
	return privateKey, publicKey, nil
}
