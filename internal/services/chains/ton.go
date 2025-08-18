package chains

import (
	"log"
	"sync"

	"wallets/internal/config"
)

type Ton struct {
	BaseChainService
	rpcClient *rpc.RpcClient
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
	// Load config
	cfg := config.Load()
	// Create RPC client with default options
	rpcClient, err :=
	if err != nil {
		log.Printf("failed to connect Ton RPC API: %v", err)
	}

	return &Ton{
		BaseChainService: BaseChainService{Code: "ton"},
		rpcClient:        rpcClient,
	}
}

// GetCode Get network code
func (ton *Ton) GetCode() string {
	return ton.Code
}

// GetUserWallet Get user HD-wallet by user id and prefix
func (ton *Ton) GetUserWallet(userId int, prefix *string) (string, error) {

}

// GetMasterWallet Get master wallet
func (ton *Ton) GetMasterWallet() (map[string]any, error) {

}

// GetTransactionCommission Get transaction commission
func (ton *Ton) GetTransactionCommission(currency string, to string, amount float64) (map[string]any, error) {

}

// GetTransactionStatus Get transaction status by hash
func (ton *Ton) GetTransactionStatus(hash string) (string, error) {

}

// SendTransaction Send transaction to the network
func (ton *Ton) SendTransaction(currency string, to string, amount float64) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (ton *Ton) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (ton *Ton) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
