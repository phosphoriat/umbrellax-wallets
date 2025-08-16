package chains

type Ton struct {
	code string
}

// NewTon Create new instance of Ton
func NewTon() *Ton {
	return &Ton{
		code: "ton",
	}
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
func (ton *Ton) SendTransaction(currency string, to string, amount float64, comment *string) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (ton *Ton) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (ton *Ton) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
