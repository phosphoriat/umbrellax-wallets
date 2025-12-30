package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"wallets/internal/models"
	"wallets/internal/repository"
	"wallets/internal/services/chains"
)

type ActionsService struct {
	actionRepo *repository.ActionRepository
	walletRepo *repository.WalletRepository
}

// NewActionsService Create new instance of ActionsService
func NewActionsService(actionRepo *repository.ActionRepository, walletRepo *repository.WalletRepository) *ActionsService {
	return &ActionsService{actionRepo, walletRepo}
}

// GetActions Get actions list with filters
func (s *ActionsService) GetActions(userId uint, aType, status string) ([]models.Action, error) {
	return s.actionRepo.GetList(userId, aType, status)
}

// RunAutoTransfers Run token auto transfers from user to master wallets for user wallets which have any tokens
func (s *ActionsService) RunAutoTransfers(userId uint) ([]any, error) {
	// Get all failed transfer actions
	actions, err := s.actionRepo.GetList(userId, "transfer", "failed")
	if err != nil {
		return nil, fmt.Errorf("[RunAutoTransfers] Failed to get failed transfers: %w", err)
	}

	// Wait group to wait for all goroutines to complete
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []any
	var errorsList []error

	for _, action := range actions {
		wg.Add(1)

		// Process every action in separate goroutines
		go func() {
			defer wg.Done()

			// Get chain service instance from action
			chainService, err := chains.GetChainService(action.Network)
			if err != nil {
				log.Printf("[RunAutoTransfers] [%s] Failed to get network chain service: %v", action.Network, err)

				mu.Lock()
				errorsList = append(errorsList, fmt.Errorf("[%s] %w", action.Network, err))
				mu.Unlock()

				return
			}

			// Get user chain wallet entity
			userWallet, err := chainService.GetUserWallet(uint32(action.UserId))
			if err != nil {
				log.Printf("[RunAutoTransfers] [%s] Failed to get user wallet for user %d: %v",
					action.Network, action.UserId, err)

				mu.Lock()
				errorsList = append(errorsList, fmt.Errorf("[%s] %w", action.Network, err))
				mu.Unlock()

				return
			}

			// Get user wallet balances
			if balances, ok := userWallet["balances"].(map[string]float64); ok {
				for currency, balance := range balances {
					if balance > 0 {
						// Try to transfer tokens from user to master wallet and get transfer fee (in native network token)
						transferFeeAmount, uHash, err := chains.TransferTokensToMasterWallet(
							context.Background(), s.walletRepo, chainService, uint32(action.UserId), currency, userWallet["address"].(string), balance,
						)
						if err != nil {
							log.Printf("[RunAutoTransfers] [%s] Failed to transfer tokens to master wallet: %v",
								action.Network, err)

							mu.Lock()
							errorsList = append(errorsList, fmt.Errorf("[%s] %w", action.Network, err))
							mu.Unlock()

							continue
						}

						mu.Lock()
						results = append(results, map[string]any{
							"time":     time.Now(),
							"user_id":  action.UserId,
							"network":  action.Network,
							"currency": currency,
							"balance":  balance,
							"hash":     uHash,
							"fee":      transferFeeAmount,
						})
						mu.Unlock()

						err = s.actionRepo.SetStatus(action.ID, "success")
						if err != nil {
							log.Printf("[RunAutoTransfers] [%s] Failed to change action %s status: %v",
								action.Network, action.ID, err)
						}
					}
				}
			}
		}()
	}

	// Waiting for all network goroutines
	wg.Wait()

	if len(errorsList) > 0 {
		return results, errors.Join(errorsList...)
	}
	return results, nil
}
