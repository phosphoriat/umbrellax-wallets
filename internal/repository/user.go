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
func (r *UserRepository) GetList() ([]models.User, error) {
	var users []models.User

	err := r.db.Order("created_at DESC").Find(&users).Error
	if err != nil {
		return nil, err
	}

	return users, nil
}

// GetNumberByUserId Get number by user id
func (r *UserRepository) GetNumberByUserId(userId int) (string, error) {
	var user models.User

	err := r.db.Where("user_id = ?", userId).
		Find(&user).Error
	if err != nil {
		return "", err
	}

	return user.Number, nil
}

// Create new user
func (r *UserRepository) Create(user *models.User) (*models.User, error) {
	err := r.db.Create(user).Error
	if err != nil {
		return nil, err
	}

	return user, nil
}
