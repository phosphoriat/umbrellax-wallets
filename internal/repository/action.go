package repository

import (
	"wallets/internal/database/clickhouse"
	"wallets/internal/models"

	"gorm.io/gorm"
)

type ActionRepository struct {
	db *gorm.DB
}

// NewActionRepository Create new instance of ActionRepository
func NewActionRepository() *ActionRepository {
	return &ActionRepository{
		db: clickhouse.GetDB(),
	}
}

// GetList Get list of all actions
func (r *ActionRepository) GetList() ([]models.Action, error) {
	var actions []models.Action

	err := r.db.Order("created_at DESC").Find(&actions).Error
	if err != nil {
		return nil, err
	}

	return actions, nil
}

// GetByUserId Get list of actions by user id
func (r *ActionRepository) GetByUserId(userId int) ([]models.Action, error) {
	var actions []models.Action

	err := r.db.Where("user_id = ?", userId).
		Order("created_at DESC").
		Find(&actions).Error
	if err != nil {
		return nil, err
	}

	return actions, nil
}

// Create new action
func (r *ActionRepository) Create(action *models.Action) (*models.Action, error) {
	err := r.db.Create(action).Error
	if err != nil {
		return nil, err
	}

	return action, nil
}
