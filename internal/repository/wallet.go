package repository

import (
	"sync"
	"time"

	"wallets/internal/database/clickhouse"
	"wallets/internal/models"

	"gorm.io/gorm"
)

type WalletRepository struct {
	db         *gorm.DB
	cache      map[string]map[string]uint
	lastUpdate map[string]time.Time
	mu         sync.RWMutex
}

// NewWalletRepository Create new instance of WalletRepository
func NewWalletRepository() *WalletRepository {
	return &WalletRepository{
		db:         clickhouse.GetDB(),
		cache:      make(map[string]map[string]uint),
		lastUpdate: make(map[string]time.Time),
	}
}

// GetAddressListByNetwork Get address list by network
// network: Network code
// Returns: map[address]userId
func (r *WalletRepository) GetAddressListByNetwork(network string) (map[string]uint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get last update cache time
	lastTime := r.lastUpdate[network]

	// First query - load all
	if lastTime.IsZero() {
		r.cache[network] = make(map[string]uint)
		lastTime = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	// Load only new items
	var wallets []models.Wallet
	err := r.db.Raw(`
		SELECT address, user_id, created_at 
		FROM wallets 
		WHERE network = ? AND created_at > ? 
		ORDER BY created_at ASC
	`, network, lastTime).Scan(&wallets).Error
	if err != nil {
		return nil, err
	}

	// Add new items in cache
	for _, w := range wallets {
		r.cache[network][w.Address] = w.UserId
	}

	// Write last update time (from last item)
	if len(wallets) > 0 {
		r.lastUpdate[network] = wallets[len(wallets)-1].CreatedAt
	}

	return r.cache[network], nil
}

// GetAddressByUserId Get address by user id and network
// userId: User id
// network: Network code
// Returns: Wallet address
func (r *WalletRepository) GetAddressByUserId(userId uint, network string) (string, error) {
	var wallet models.Wallet

	err := r.db.Raw(`SELECT * FROM wallets FINAL WHERE user_id = ? AND network = ? LIMIT 1`, userId, network).
		Scan(&wallet).Error
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

	err := r.db.Raw(`SELECT * FROM wallets FINAL WHERE address = ? LIMIT 1`, address).
		Scan(&wallet).Error
	if err != nil {
		return 0, err
	}

	return wallet.UserId, nil
}

// CreateIfNotExists Create wallet if not exists
// Returns: created (true if new wallet was created)
func (r *WalletRepository) CreateIfNotExists(wallet *models.Wallet) (bool, error) {
	var exists bool

	err := r.db.Raw(`SELECT EXISTS(SELECT 1 FROM wallets WHERE user_id = ? AND network = ?)`,
		wallet.UserId, wallet.Network).Scan(&exists).Error
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	err = r.db.Create(wallet).Error
	if err != nil {
		return false, err
	}

	// Update address list cache
	r.mu.Lock()
	if r.cache[wallet.Network] != nil {
		r.cache[wallet.Network][wallet.Address] = wallet.UserId
	}
	r.mu.Unlock()

	return true, nil
}
