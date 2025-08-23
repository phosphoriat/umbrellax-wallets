package services

import (
	"wallets/internal/services/chains"
)

type TransactionsService struct{}

// NewTransactionsService Create new instance of TransactionsService
func NewTransactionsService() *TransactionsService {
	return &TransactionsService{}
}

// GetTransactionCommission Calc transaction commission
func (s *TransactionsService) GetTransactionCommission(network, currency, to string, amount float64, memo string) (map[string]any, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	res, err := chainService.GetTransactionCommission(currency, to, amount, memo)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetTransactionStatus Get transaction status by hash
func (s *TransactionsService) GetTransactionStatus(network, hash, toAddr string) (string, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return "", err
	}

	status, err := chainService.GetTransactionStatus(hash, toAddr)
	if err != nil {
		return "", err
	}

	return status, nil
}

// SendWithdrawal Send new withdraw to the queue and return queue_id
func (s *TransactionsService) SendWithdrawal(network, currency, to string, amount float64, memo string) (map[string]any, error) {
	// Get chain service instance from registry
	_, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	res, err := chains.CreateWithdrawRequest(currency, network, to, amount, memo)
	if err != nil {
		return nil, err
	}

	return res, nil
}
