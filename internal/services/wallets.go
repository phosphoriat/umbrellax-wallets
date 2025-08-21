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
func (s *WalletsService) GetUserWallet(userId int, network string) (string, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return "", err
	}

	address, err := chainService.GetUserWallet(userId)
	if err != nil {
		return "", err
	}

	return address, nil
}

// GetMasterWallet Get master wallet info by network
func (s *WalletsService) GetMasterWallet(network string) (map[string]any, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	res, err := chainService.GetMasterWallet()
	if err != nil {
		return nil, err
	}

	return res, nil
}
