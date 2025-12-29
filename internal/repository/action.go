package repository

import (
	"wallets/internal/database/clickhouse"
	"wallets/internal/models"

	"github.com/google/uuid"
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

// GetList Get list of all actions with filters
// userId: User id
// aType: Action type
// status: Action status
// Returns: Action models list
func (r *ActionRepository) GetList(userId uint, aType, status string) ([]models.Action, error) {
	var actions []models.Action

	query := r.db.Model(&models.Action{})
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
	}
	if aType != "" {
		query = query.Where("type = ?", aType)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	err := query.Order("id DESC").
		Find(&actions).Error
	if err != nil {
		return nil, err
	}

	return actions, nil
}

// Create new action
// Returns: Created Action model
func (r *ActionRepository) Create(action *models.Action) (*models.Action, error) {
	if action.ID == "" {
		action.ID = uuid.New().String()
	}

	err := r.db.Create(action).Error
	if err != nil {
		return nil, err
	}

	return action, nil
}

// SetStatus Update status via insert (ReplacingMergeTree handles merge)
func (r *ActionRepository) SetStatus(id, status string) error {
	var action models.Action

	err := r.db.Where("id = ?", id).
		First(&action).Error
	if err != nil {
		return err
	}

	action.Status = status

	return r.db.Create(&action).Error
}
