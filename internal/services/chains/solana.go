package chains

type Solana struct {
	code string
}

// NewSolana Create new instance of Solana
func NewSolana() *Solana {
	return &Solana{
		code: "solana",
	}
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
func (sol *Solana) SendTransaction(currency string, to string, amount float64, comment *string) (map[string]any, error) {

}

// ProcessInboundTransactions Process inbound transactions
func (sol *Solana) ProcessInboundTransactions() {

}

// @private deriveAddress
// Get user HD-wallet address from master wallet address
func (sol *Solana) deriveAddress(masterAddress string, userId int64, prefix *string) (string, error) {

}
