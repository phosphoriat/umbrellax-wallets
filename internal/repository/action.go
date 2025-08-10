package repository

import (
	"wallets/internal/database"
	"wallets/internal/models"

	"gorm.io/gorm"
)

type (
	ActionRepository struct {
		db *gorm.DB
	}
)

// NewActionRepository Create new instance of ActionRepository
func NewActionRepository() *ActionRepository {
	return &ActionRepository{
		db: database.GetConnection(),
	}
}

// GetList Get list of all actions
func (r *ActionRepository) GetList() ([]*models.Action, error) {
	var actions []*models.Action

	err := r.db.Order("created_at DESC").Find(&actions).Error
	if err != nil {
		return nil, err
	}

	return actions, nil
}

// GetByUserId Get list of actions for user id
func (r *ActionRepository) GetByUserId(userId int) ([]*models.Action, error) {
	var actions []*models.Action

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

// Update action by id
func (r *ActionRepository) Update(action *models.Action) (*models.Action, error) {
	res := r.db.Model(action).Updates(action)
	if res.Error != nil {
		return nil, res.Error
	}

	// Check affected rows
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	// Load updated entity
	var updatedAction models.Action
	res = r.db.First(&updatedAction, action.Id)
	if res.Error != nil {
		return nil, res.Error
	}

	return &updatedAction, nil
}

// Delete action by id
func (r *ActionRepository) Delete(id int) error {
	result := r.db.Delete(&models.Action{}, id)
	if result.Error != nil {
		return result.Error
	}

	// Check affected rows
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}
