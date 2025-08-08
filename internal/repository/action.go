package repository

import "wallets/internal/models"

type ActionRepository interface {
	GetList() ([]*models.Action, error)
	GetByID(id int) (*models.Action, error)
	Create(action *models.Action) (*models.Action, error)
	Update(id int, action *models.Action) (*models.Action, error)
	Delete(id int) error
}
