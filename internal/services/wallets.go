package services

import (
	"fmt"
	"wallets/internal/services/chains"
)

type WalletsService struct {
	chains map[string]chains.ChainService
}

// NewWalletsService Create new instance of WalletsService
func NewWalletsService(chains map[string]chains.ChainService) *WalletsService {
	return &WalletsService{chains}
}

// GetUserWallet Get user HD-wallet by user id
func (service *WalletsService) GetUserWallet(userId int, network string) (map[string]string, error) {
	// Get chain instance from injected dependencies
	chainService, exists := service.chains[network]
	if !exists {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	address, err := chainService.GetUserWallet(userId, nil)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"address": address,
	}, nil
}

// GetMasterWallet Get master wallet info by network
func (service *WalletsService) GetMasterWallet(network string) (map[string]any, error) {
	// Get chain instance from injected dependencies
	chainService, exists := service.chains[network]
	if !exists {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	wallet, err := chainService.GetMasterWallet()
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"wallet": wallet,
	}, nil
}
