package chains

type Ethereum struct {
	code string
}

// NewEthereum Create new instance of Ethereum
func NewEthereum() *Ethereum {
	return &Ethereum{
		code: "ethereum",
	}
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
func (eth *Ethereum) SendTransaction(currency string, to string, amount float64, comment *string) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (eth *Ethereum) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (eth *Ethereum) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
