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

// GetAddressListByNetwork Get address list by network
// network: Network code
// Returns: map[address]userId
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
// userId: User id
// network: Network code
// Returns: Wallet address
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
// address: User wallet address
// Returns: User id
func (r *WalletRepository) GetUserIdByAddress(address string) (uint, error) {
	var wallet models.Wallet

	err := r.db.Where("address = ?", address).
		First(&wallet).Error
	if err != nil {
		return 0, err
	}

	return wallet.UserId, nil
}

// Create new wallet (ReplacingMergeTree handles duplicates)
// Returns: Created Wallet model
func (r *WalletRepository) Create(wallet *models.Wallet) (*models.Wallet, error) {
	err := r.db.Create(wallet).Error
	if err != nil {
		return nil, err
	}

	return wallet, nil
}
