package chains

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"sync"
	"wallets/internal/services/api/aml"

	"wallets/internal/config"
	"wallets/internal/database/vault"

	"github.com/fbsobreira/gotron-sdk/pkg/address"
	"github.com/fbsobreira/gotron-sdk/pkg/client"
)

// Tron RPC connection struct
type Tron struct {
	BaseChainService
	Client *client.GrpcClient
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
	cfg := config.Load()

	// Create new RPC client
	rpcClient := client.NewGrpcClient(cfg.TronRpcUrl)
	err := rpcClient.Start(client.GRPCInsecure())
	if err != nil {
		log.Printf("failed to connect Tron RPC API: %v", err)
	}

	return &Tron{
		BaseChainService: BaseChainService{
			NetworkCode: "tron",
			AmlTokenId:  "",
		},
		Client: rpcClient,
	}
}

// GetCode Get network code
func (tron *Tron) GetCode() string {
	return tron.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id and prefix (if passed)
func (tron *Tron) GetUserWallet(userId int, prefix *string) (string, error) {
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.Code)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	return tron.deriveAddress(masterData["address"].(string), userId, prefix), nil
}

// GetMasterWallet Get master wallet
func (tron *Tron) GetMasterWallet() (map[string]any, error) {
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.Code)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}

	wallet := map[string]any{
		"address": masterData["address"].(string),
		"balance": 0,
	}

	return wallet, nil
}

// GetTransactionCommission Get transaction commission
func (tron *Tron) GetTransactionCommission(currency string, to string, amount float64) (map[string]any, error) {
	return map[string]any{}, nil
}

// GetTransactionStatus Get transaction status by hash
func (tron *Tron) GetTransactionStatus(hash string) (string, error) {
	tx, err := tron.Client.GetTransactionByID(hash)
	if err != nil {
		return "", fmt.Errorf("failed to get transaction: %w", err)
	}

	status := ""

	return status, nil
}

// SendTransaction Send transaction to the network
func (tron *Tron) SendTransaction(currency string, to string, amount float64) (map[string]any, error) {
	// Send new transactions on chain and get hash
	hash := ""

	// @TODO AML Check transaction by hash and get aml id
	amlId, err := aml.GetClient().CheckTransaction(tron.NetworkCode, tron.AmlTokenId, hash, to)
	if err != nil {
		return nil, fmt.Errorf("failed to AML check transaction: %w", err)
	}

	return map[string]any{
		"hash":   hash,
		"aml_id": amlId,
	}, nil
}

// ProcessInboundTransactions Process inbound transactions
func (tron *Tron) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (tron *Tron) deriveAddress(masterAddress string, userId int, prefix *string) string {
	// Union master wallet address with stringify user id (and add prefix, if passed)
	combinedData := masterAddress
	if prefix != nil {
		combinedData = *prefix + combinedData
	}
	combinedData += strconv.FormatInt(int64(userId), 10)

	// Make SHA256 hash
	hash := sha256.Sum256([]byte(combinedData))
	hashHex := hex.EncodeToString(hash[:])

	// Take first 40 symbols of hash
	addressHex := "41" + hashHex[:40]
	// Convert hex to bytes
	addressBytes, _ := hex.DecodeString(addressHex)

	// Convert to base58 (default TRON address)
	return address.Address(addressBytes).String()
}
