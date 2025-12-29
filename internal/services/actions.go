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
	// Get all refilled user wallets
	wallets, err := s.walletRepo.GetRefilled(userId)
	if err != nil {
		return nil, fmt.Errorf("[RunAutoTransfers] Failed to get refilled user wallets: %w", err)
	}

	// Wait group to wait for all network goroutines to complete
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []any
	var errorsList []error

	for _, wallet := range wallets {
		wg.Add(1)

		// Process every user wallet in separate goroutines
		go func() {
			defer wg.Done()

			// Get chain service instance for wallet network
			chainService, err := chains.GetChainService(wallet.Network)
			if err != nil {
				log.Printf("[RunAutoTransfers] [%s] Failed to get network chain service: %v", wallet.Network, err)

				mu.Lock()
				errorsList = append(errorsList, fmt.Errorf("[%s] %w", wallet.Network, err))
				mu.Unlock()

				return
			}

			// Get user chain wallet entity
			chainWallet, err := chainService.GetUserWallet(uint32(wallet.UserId))
			if err != nil {
				log.Printf("[RunAutoTransfers] [%s] Failed to get user wallet for user %d: %v",
					wallet.Network, wallet.UserId, err)

				mu.Lock()
				errorsList = append(errorsList, fmt.Errorf("[%s] %w", wallet.Network, err))
				mu.Unlock()

				return
			}

			// Get user wallet balances
			if balances, ok := chainWallet["balances"].(map[string]float64); ok {
				for token, balance := range balances {
					if balance > 0 {
						// Try to transfer tokens from user to master wallet and get transfer fee (in native network token)
						transferFeeAmount, err := chains.TransferTokensToMasterWallet(
							context.Background(), chainService, uint32(wallet.UserId), token, wallet.Address, balance,
						)
						if err != nil {
							log.Printf("[RunAutoTransfers] [%s] Failed to transfer tokens to master wallet: %v",
								wallet.Network, err)

							mu.Lock()
							errorsList = append(errorsList, fmt.Errorf("[%s] %w", wallet.Network, err))
							mu.Unlock()

							continue
						}

						mu.Lock()
						results = append(results, map[string]any{
							"time":    time.Now(),
							"user_id": wallet.UserId,
							"network": wallet.Network,
							"token":   token,
							"balance": balance,
							"fee":     transferFeeAmount,
						})
						mu.Unlock()
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
