package chains

import (
	"log"
	"sync"

	"wallets/internal/config"

	"github.com/ethereum/go-ethereum/ethclient"
)

// Ethereum RPC connection struct
type Ethereum struct {
	BaseChainService
	Client *ethclient.Client
}

// Global package vars
var (
	ethInstance *Ethereum
	ethOnce     sync.Once
)

// GetEthereum Get Ethereum chain instance
func GetEthereum() *Ethereum {
	ethOnce.Do(func() {
		ethInstance = newEthereum()
	})
	return ethInstance
}

// @private newEthereum Create new instance of Ethereum (private constructor)
func newEthereum() *Ethereum {
	// Load config
	cfg := config.Load()

	// Create new RPC client
	ethClient, err := ethclient.Dial(cfg.EthRpcUrl)
	if err != nil {
		log.Printf("failed to connect Ethereum RPC API: %v", err)
	}

	return &Ethereum{
		BaseChainService: BaseChainService{
			NetworkCode: "ethereum",
			AmlTokenId:  "",
		},
		Client: ethClient,
	}
}

// GetCode Get network code
func (eth *Ethereum) GetCode() string {
	return eth.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id and prefix
func (eth *Ethereum) GetUserWallet(userId int, prefix *string) (string, error) {

}

// GetMasterWallet Get master wallet
func (eth *Ethereum) GetMasterWallet() (map[string]any, error) {

}

// GetTransactionCommission Get transaction commission
func (eth *Ethereum) GetTransactionCommission(currency string, to string, amount float64) (map[string]any, error) {

}

// GetTransactionStatus Get transaction status by hash
func (eth *Ethereum) GetTransactionStatus(hash string) (string, error) {

}

// SendTransaction Send transaction to the network
func (eth *Ethereum) SendTransaction(currency string, to string, amount float64) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (eth *Ethereum) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (eth *Ethereum) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
