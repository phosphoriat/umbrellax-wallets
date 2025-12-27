package services

import (
	"wallets/internal/models"
	"wallets/internal/repository"
	"wallets/internal/services/chains"
)

type WalletsService struct {
	walletRepo *repository.WalletRepository
}

// NewWalletsService Create new instance of WalletsService
func NewWalletsService(walletRepo *repository.WalletRepository) *WalletsService {
	return &WalletsService{
		walletRepo: walletRepo,
	}
}

// GetUserWallet Get user HD-wallet by user id
func (s *WalletsService) GetUserWallet(userId uint, network string) (string, error) {
	// Check if address exists in DB
	existingAddress, err := s.walletRepo.GetAddressByUserId(userId)
	if err == nil && existingAddress != "" {
		return existingAddress, nil
	}

	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return "", err
	}

	// Generate new address
	address, err := chainService.GetUserWallet(uint32(userId))
	if err != nil {
		return "", err
	}

	// Save address in DB, if not exists
	_, err = s.walletRepo.Create(&models.Wallet{
		UserId:  userId,
		Network: network,
		Address: address,
	})
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
