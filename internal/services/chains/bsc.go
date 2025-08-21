package chains

import (
	"log"
	"sync"

	"wallets/internal/config"

	"github.com/ethereum/go-ethereum/ethclient"
)

// Bsc RPC connection struct
type Bsc struct {
	BaseChainService
	Client *ethclient.Client
}

// Global package vars
var (
	bscInstance *Bsc
	bscOnce     sync.Once
)

// GetBsc Get Bsc chain instance
func GetBsc() *Bsc {
	bscOnce.Do(func() {
		bscInstance = newBsc()
	})
	return bscInstance
}

// @private newBsc Create new instance of Bsc (private constructor)
func newBsc() *Bsc {
	// Load config
	cfg := config.Load()

	// Create new RPC client
	ethClient, err := ethclient.Dial(cfg.BscRrcUrl)
	if err != nil {
		log.Printf("failed to connect Bsc RPC API: %v", err)
	}

	return &Bsc{
		BaseChainService: BaseChainService{
			NetworkCode: "bsc",
			AmlTokenId:  "",
		},
		Client: ethClient,
	}
}

// GetCode Get network code
func (bsc *Bsc) GetCode() string {
	return bsc.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
func (bsc *Bsc) GetUserWallet(userId int) (string, error) {

}

// GetMasterWallet Get master wallet
func (bsc *Bsc) GetMasterWallet() (map[string]any, error) {

}

// GetTransactionCommission Get transaction commission
func (bsc *Bsc) GetTransactionCommission(currency string, to string, amount float64) (map[string]any, error) {

}

// GetTransactionStatus Get transaction status by hash
func (bsc *Bsc) GetTransactionStatus(hash string) (string, error) {

}

// SendTransaction Send transaction to the network
func (bsc *Bsc) SendTransaction(currency string, to string, amount float64) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (bsc *Bsc) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (bsc *Bsc) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
