package chains

import (
	"log"
	"sync"

	"wallets/internal/config"

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
	cfg := config.Load()

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

	return &Bitcoin{
		BaseChainService: BaseChainService{Code: "bitcoin"},
		Client:           client,
	}
}

// GetCode Get network code
func (btc *Bitcoin) GetCode() string {
	return btc.Code
}

// GetUserWallet Get user HD-wallet by user id and prefix
func (btc *Bitcoin) GetUserWallet(userId int, prefix *string) (string, error) {

}

// GetMasterWallet Get master wallet
func (btc *Bitcoin) GetMasterWallet() (map[string]any, error) {

}

// GetTransactionCommission Get transaction commission
func (btc *Bitcoin) GetTransactionCommission(currency string, to string, amount float64) (map[string]any, error) {

}

// GetTransactionStatus Get transaction status by hash
func (btc *Bitcoin) GetTransactionStatus(hash string) (string, error) {

}

// SendTransaction Send transaction to the network
func (btc *Bitcoin) SendTransaction(currency string, to string, amount float64) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (btc *Bitcoin) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (btc *Bitcoin) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
