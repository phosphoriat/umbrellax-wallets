package chains

type Bitcoin struct {
	code string
}

// NewBitcoin Create new instance of Tron
func NewBitcoin() *Bitcoin {
	return &Bitcoin{
		code: "bitcoin",
	}
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
func (btc *Bitcoin) SendTransaction(currency string, to string, amount float64, comment *string) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (btc *Bitcoin) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (btc *Bitcoin) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
