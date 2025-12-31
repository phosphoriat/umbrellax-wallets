package repository

import (
	"wallets/internal/database/clickhouse"
	"wallets/internal/models"

	"gorm.io/gorm"
)

type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository Create new instance of UserRepository
func NewUserRepository() *UserRepository {
	return &UserRepository{
		db: clickhouse.GetDB(),
	}
}

// GetList Get list of all users
// Returns: User models list
func (r *UserRepository) GetList(limit, offset int) ([]models.User, error) {
	var users []models.User

	err := r.db.Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&users).Error
	if err != nil {
		return nil, err
	}

	return users, nil
}

// GetUserIdByNumber Get user id by number
// Returns: User id
func (r *UserRepository) GetUserIdByNumber(number string) (uint, error) {
	var user models.User

	err := r.db.Where("number = ?", number).
		First(&user).Error
	if err != nil {
		return 0, err
	}

	return user.UserId, nil
}

// CreateIfNotExists Create user if not exists
// Returns: created (true if new user was created)
func (r *UserRepository) CreateIfNotExists(user *models.User) (bool, error) {
	var exists bool

	err := r.db.Raw(`SELECT EXISTS(SELECT 1 FROM users WHERE user_id = ?)`,
		user.UserId).Scan(&exists).Error
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	err = r.db.Create(user).Error
	if err != nil {
		return false, err
	}

	return true, nil
}
