package services

import (
	"fmt"
	"wallets/internal/services/chains"
)

type TransactionsService struct {
	chains map[string]chains.ChainService
}

// NewTransactionsService Create new instance of TransactionsService
func NewTransactionsService(chains map[string]chains.ChainService) *TransactionsService {
	return &TransactionsService{chains}
}

// GetTransactionCommission Calc transaction commission
func (service *TransactionsService) GetTransactionCommission(
	network string, currency string, to string, amount float64,
) (map[string]any, error) {
	// Get chain instance from injected dependencies
	chainService, exists := service.chains[network]
	if !exists {
		return nil, fmt.Errorf("unsupported network: %s", network)
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
func (service *TransactionsService) GetTransactionStatus(network string, hash string) (map[string]string, error) {
	// Get chain instance from injected dependencies
	chainService, exists := service.chains[network]
	if !exists {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	status, err := chainService.GetTransactionStatus(hash)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"status": status,
	}, nil
}

// SendTransaction Send new transaction to the network and return hash
func (service *TransactionsService) SendTransaction(
	network string, currency string, to string, amount float64, comment *string,
) (map[string]any, error) {
	// Get chain instance from injected dependencies
	chainService, exists := service.chains[network]
	if !exists {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	transaction, err := chainService.SendTransaction(currency, to, amount, comment)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"transaction": transaction,
	}, nil
}
