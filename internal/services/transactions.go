package services

type TransactionsService struct{}

func NewTransactionsService() *TransactionsService {
	return &TransactionsService{}
}

// GetTransactionCommission
// Calc transaction commission
func (s *TransactionsService) GetTransactionCommission(
	network string, currency string, to string, amount float64,
) map[string]string {
	arr := make(map[string]string)
	return arr
}

// GetTransactionStatus
// Get transaction status by hash
func (s *TransactionsService) GetTransactionStatus(hash string) map[string]string {
	arr := make(map[string]string)
	return arr
}

// SendTransaction
// Send new transaction to the network and return hash
func (s *TransactionsService) SendTransaction(
	network string, currency string, to string, amount float64, comment *string,
) map[string]string {
	arr := make(map[string]string)
	return arr
}
