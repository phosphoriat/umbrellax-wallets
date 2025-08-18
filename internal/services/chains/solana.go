package chains

import (
	"sync"

	"wallets/internal/config"

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
	cfg := config.Load()

	// Create RPC client
	client := rpc.New(cfg.SolRpcUrl)

	return &Solana{
		BaseChainService: BaseChainService{Code: "solana"},
		rpcClient:        client,
	}
}

// GetCode Get network code
func (sol *Solana) GetCode() string {
	return sol.Code
}

// GetUserWallet Get user HD-wallet by user id and prefix
func (sol *Solana) GetUserWallet(userId int, prefix *string) (string, error) {

}

// GetMasterWallet Get master wallet
func (sol *Solana) GetMasterWallet() (map[string]any, error) {

}

// GetTransactionCommission Get transaction commission
func (sol *Solana) GetTransactionCommission(currency string, to string, amount float64) (map[string]any, error) {

}

// GetTransactionStatus Get transaction status by hash
func (sol *Solana) GetTransactionStatus(hash string) (string, error) {

}

// SendTransaction Send transaction to the network
func (sol *Solana) SendTransaction(currency string, to string, amount float64) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (sol *Solana) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (sol *Solana) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
