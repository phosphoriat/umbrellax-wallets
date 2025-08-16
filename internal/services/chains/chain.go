package chains

type ChainService interface {
	GetUserWallet(userId int, prefix *string) (string, error)
	GetMasterWallet() (map[string]any, error)
	GetTransactionCommission(currency string, to string, amount float64) (map[string]any, error)
	GetTransactionStatus(hash string) (string, error)
	SendTransaction(currency string, to string, amount float64, comment *string) (map[string]any, error)
	ProcessInboundTransactions()
}
