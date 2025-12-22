package services

import (
	"wallets/internal/models"
	"wallets/internal/repository"
)

type ActionService struct {
	actionRepo repository.ActionRepository
}

// NewActionService Create new instance of ActionService
func NewActionService(actionRepo repository.ActionRepository) *ActionService {
	return &ActionService{actionRepo}
}

// GetList Get list of all actions
func (s *ActionService) GetList() ([]models.Action, error) {
	return s.actionRepo.GetList()
}

// GetByUserId Get list of actions for user id
func (s *ActionService) GetByUserId(userId int) ([]models.Action, error) {
	return s.actionRepo.GetByUserId(userId)
}

// Create new action
func (s *ActionService) Create(data *models.Action) (*models.Action, error) {
	return s.actionRepo.Create(data)
}
