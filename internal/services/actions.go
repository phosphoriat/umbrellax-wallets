package services

import (
	"wallets/internal/models"
	"wallets/internal/repository"
)

type ActionService struct {
	actionRepo repository.ActionRepository
}

func NewActionService(actionRepo repository.ActionRepository) *ActionService {
	return &ActionService{actionRepo: actionRepo}
}

func (s *ActionService) GetUserByID(id int) (*models.Action, error) {
	return s.actionRepo.GetByID(id)
}
