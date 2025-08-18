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
func (s *TransactionsService) GetTransactionCommission(network, currency, to string, amount float64) (map[string]any, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	commission, err := chainService.GetTransactionCommission(currency, to, amount)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"commission": commission,
	}, nil
}

// GetTransactionStatus Get transaction status by hash
func (s *TransactionsService) GetTransactionStatus(network, hash string) (map[string]any, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	status, err := chainService.GetTransactionStatus(hash)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"status": status,
	}, nil
}

// SendWithdrawal Send new withdraw to the queue and return queue_id
func (s *TransactionsService) SendWithdrawal(network, currency, to string, amount float64) (map[string]any, error) {
	// Get chain service instance from registry
	_, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	res, err := chains.CreateBybitWithdrawRequest(currency, network, to, amount)
	if err != nil {
		return nil, err
	}

	return res, nil
}
