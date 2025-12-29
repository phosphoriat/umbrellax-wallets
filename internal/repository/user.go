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
func (r *UserRepository) GetList() ([]models.User, error) {
	var users []models.User

	err := r.db.Order("created_at DESC").Find(&users).Error
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

// Create new user (ReplacingMergeTree handles duplicates)
// Returns: Created User model
func (r *UserRepository) Create(user *models.User) (*models.User, error) {
	err := r.db.Create(user).Error
	if err != nil {
		return nil, err
	}

	return user, nil
}
