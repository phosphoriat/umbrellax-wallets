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

// GetUserIdByNumber Get user id by number
func (r *UserRepository) GetUserIdByNumber(number string) (uint, error) {
	var user models.User

	err := r.db.Where("number = ?", number).
		Find(&user).Error
	if err != nil {
		return 0, err
	}

	return user.UserId, nil
}

// FirstOrCreate Create new user, if not exists
func (r *UserRepository) FirstOrCreate(user *models.User) (*models.User, error) {
	err := r.db.Where("user_id = ?", user.UserId).
		FirstOrCreate(user).Error
	if err != nil {
		return nil, err
	}

	return user, nil
}
