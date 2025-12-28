package services

import (
	"wallets/internal/models"
	"wallets/internal/repository"
	"wallets/internal/services/chains"
)

type WalletsService struct {
	walletRepo *repository.WalletRepository
	userRepo   *repository.UserRepository
}

// NewWalletsService Create new instance of WalletsService
func NewWalletsService(walletRepo *repository.WalletRepository, userRepo *repository.UserRepository) *WalletsService {
	return &WalletsService{
		walletRepo: walletRepo,
		userRepo:   userRepo,
	}
}

// GetUserWalletAddress Get user HD-wallet address by user id
func (s *WalletsService) GetUserWalletAddress(userId uint, userNumber, network string) (string, error) {
	// Check if address exists in DB
	existingAddress, err := s.walletRepo.GetAddressByUserId(userId, network)
	if err == nil && existingAddress != "" {
		return existingAddress, nil
	}

	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return "", err
	}

	// Generate new address
	address, err := chainService.GetUserWalletAddress(uint32(userId))
	if err != nil {
		return "", err
	}

	// Save user in users DB, if not exists
	_, err = s.userRepo.FirstOrCreate(&models.User{
		UserId: userId,
		Number: userNumber,
	})
	if err != nil {
		return "", err
	}

	// Save address in wallets DB, if not exists
	_, err = s.walletRepo.FirstOrCreate(&models.Wallet{
		UserId:  userId,
		Network: network,
		Address: address,
	})
	if err != nil {
		return "", err
	}

	return address, nil
}

// GetUserWalletInfo Get user wallet info by network
func (s *WalletsService) GetUserWalletInfo(userId uint, network string) (map[string]any, error) {
	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return nil, err
	}

	res, err := chainService.GetUserWallet(uint32(userId))
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetMasterWalletInfo Get master wallet info by network
func (s *WalletsService) GetMasterWalletInfo(network string) (map[string]any, error) {
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
