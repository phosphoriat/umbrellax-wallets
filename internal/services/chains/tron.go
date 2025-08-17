package chains

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"

	"wallets/internal/config"
	"wallets/internal/database/vault"

	"github.com/fbsobreira/gotron-sdk/pkg/address"
	"github.com/fbsobreira/gotron-sdk/pkg/client"
)

type Tron struct {
	code      string
	rpcClient *client.GrpcClient
}

// NewTron Create new instance of Tron
func NewTron() *Tron {
	// Load config
	cfg := config.Load()
	// Create client with default options
	rpcClient := client.NewGrpcClient(cfg.TronRpcUrl)

	// Start the connection to the RPC
	err := rpcClient.Start(client.GRPCInsecure())
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}
	defer rpcClient.Stop()

	return &Tron{
		code:      "tron",
		rpcClient: rpcClient,
	}
}

// GetUserWallet Get user HD-wallet by user id and prefix
func (tron *Tron) GetUserWallet(userId int, prefix *string) (string, error) {
	masterData, err := vault.GetSecret("/master/" + tron.code)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	return tron.deriveAddress(masterData["address"].(string), userId, prefix), nil
}

// GetMasterWallet Get master wallet
func (tron *Tron) GetMasterWallet() (map[string]any, error) {
	masterData, err := vault.GetSecret("/master/" + tron.code)
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
	tx, err := tron.rpcClient.GetTransactionByID(hash)
	if err != nil {
		return "", fmt.Errorf("failed to get transaction: %w", err)
	}

	status := ""

	return status, nil
}

// SendTransaction Send transaction to the network
func (tron *Tron) SendTransaction(currency string, to string, amount float64, comment *string) (map[string]any, error) {
	return map[string]any{}, nil
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
