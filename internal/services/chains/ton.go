package chains

import (
	"sync"

	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/ton"
)

type Ton struct {
	BaseChainService
	Client ton.APIClientWrapped
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

	return &Ton{
		BaseChainService: BaseChainService{
			NetworkCode: "ton",
			AmlTokenId:  "",
		},
		Client: apiClient,
	}
}

// GetCode Get network code
func (ton *Ton) GetCode() string {
	return ton.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
func (ton *Ton) GetUserWallet(userId int) (string, error) {

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
