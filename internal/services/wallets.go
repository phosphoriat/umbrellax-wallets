package services

import (
	"wallets/internal/services/chains"
)

type WalletsService struct{}

// NewWalletsService Create new instance of WalletsService
func NewWalletsService() *WalletsService {
	return &WalletsService{}
}

// GetUserWallet Get user HD-wallet by user id
func (s *WalletsService) GetUserWallet(userId int, network string) (map[string]any, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	address, err := chainService.GetUserWallet(userId, nil)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"address": address,
	}, nil
}

// GetMasterWallet Get master wallet info by network
func (s *WalletsService) GetMasterWallet(network string) (map[string]any, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	wallet, err := chainService.GetMasterWallet()
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"wallet": wallet,
	}, nil
}
