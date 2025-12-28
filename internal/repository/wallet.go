package repository

import (
	"wallets/internal/database/clickhouse"
	"wallets/internal/models"

	"gorm.io/gorm"
)

type WalletRepository struct {
	db *gorm.DB
}

// NewWalletRepository Create new instance of WalletRepository
func NewWalletRepository() *WalletRepository {
	return &WalletRepository{
		db: clickhouse.GetDB(),
	}
}

// GetList Get list of all wallets
func (r *WalletRepository) GetList() ([]models.Wallet, error) {
	var wallets []models.Wallet

	err := r.db.Order("created_at DESC").Find(&wallets).Error
	if err != nil {
		return nil, err
	}

	return wallets, nil
}

// GetAddressListByNetwork Get address list by network
func (r *WalletRepository) GetAddressListByNetwork(network string) (map[string]uint, error) {
	var wallets []models.Wallet

	err := r.db.Where("network = ?", network).
		Order("created_at DESC").
		Find(&wallets).Error
	if err != nil {
		return nil, err
	}

	addressList := make(map[string]uint, len(wallets))
	for _, wallet := range wallets {
		addressList[wallet.Address] = wallet.UserId
	}

	return addressList, nil
}

// GetAddressByUserId Get address by user id and network
func (r *WalletRepository) GetAddressByUserId(userId uint, network string) (string, error) {
	var wallet models.Wallet

	err := r.db.Where("user_id = ?", userId).
		Where("network = ?", network).
		First(&wallet).Error
	if err != nil {
		return "", err
	}

	return wallet.Address, nil
}

// GetUserIdByAddress Get user id by address
func (r *WalletRepository) GetUserIdByAddress(address string) (uint, error) {
	var wallet models.Wallet

	err := r.db.Where("address = ?", address).
		First(&wallet).Error
	if err != nil {
		return 0, err
	}

	return wallet.UserId, nil
}

// FirstOrCreate Create new wallet, if not exists
func (r *WalletRepository) FirstOrCreate(wallet *models.Wallet) (*models.Wallet, error) {
	err := r.db.Where("user_id = ?", wallet.UserId).
		Where("network = ?", wallet.Network).
		FirstOrCreate(wallet).Error
	if err != nil {
		return nil, err
	}

	return wallet, nil
}
