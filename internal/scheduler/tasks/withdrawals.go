package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/models"
	"wallets/internal/repository"
	"wallets/internal/services/api/bybit"
	"wallets/internal/services/chains"
	"wallets/internal/utils"
)

type WithdrawalsTask struct {
	BaseTask
	bybit                    *bybit.HTTPClient
	redis                    *redis.Client
	RedisBybitWithdrawalsKey string
	RedisWithdrawalsKey      string
}

func NewWithdrawalsTask() *WithdrawalsTask {
	cfg, _ := config.Load()

	return &WithdrawalsTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			timeout:     30 * time.Second,
			name:        "withdrawals",
			description: "Process withdrawals",
		},
		bybit:                    bybit.GetClient(),
		redis:                    redis.GetClient(),
		RedisBybitWithdrawalsKey: cfg.RedisBybitWithdrawalsKey,
		RedisWithdrawalsKey:      cfg.RedisWithdrawalsKey,
	}
}

func (t *WithdrawalsTask) Execute(ctx context.Context) error {
	actionRepo := repository.NewActionRepository()

	// Get all items from hash
	values, err := t.redis.HGetAll(ctx, t.RedisBybitWithdrawalsKey)
	if err != nil {
		return fmt.Errorf("[WithdrawalsTask] Read hash error '%s': %w", t.RedisBybitWithdrawalsKey, err)
	}

	// Process all items from hash
	for key, value := range values {
		isPending, err := t.processItem(ctx, key, value, actionRepo)
		if isPending {
			continue
		}
		if err != nil {
			log.Printf("[WithdrawalsTask] Process item error: %v", err)
		}

		// Delete item from bybit withdrawals Redis hash
		t.redis.HDel(ctx, t.RedisBybitWithdrawalsKey, key)
	}

	return nil
}

// @private processItem Process hash item
func (t *WithdrawalsTask) processItem(ctx context.Context, requestId, itemStr string, actionRepo *repository.ActionRepository) (bool, error) {
	// Parse JSON item string
	var item chains.WithdrawalTransaction
	if err := json.Unmarshal([]byte(itemStr), &item); err != nil {
		return false, fmt.Errorf("failed to unmarshal bybit withdrawal tx item: %w", err)
	}
	if (item.BybitId == "" && item.Currency != "UMBRELLA") || item.Currency == "" || item.Network == "" || item.To == "" || item.Amount <= 0 {
		return false, fmt.Errorf("invalid bybit withdrawal tx item data: %+v", item)
	}

	var status string
	if item.Currency != "UMBRELLA" {
		// Check bybit withdrawal tx status
		resultMap, err := t.bybit.GetWithdrawalRecords(ctx, map[string]any{"withdrawID": item.BybitId})
		if err != nil {
			return false, err
		}

		statusValue, _ := utils.DataGet(resultMap, "0.status")
		status, _ = statusValue.(string)

		switch status {
		case "SecurityCheck", "Pending", "MoreInformationRequired":
			return true, fmt.Errorf("bybit withdrawal %s still pending (status: %s)", item.BybitId, status)
		default:
			log.Printf("[WithdrawalsTask] Bybit withdrawal [%s] status: %s", item.BybitId, status)
		}
	}

	var (
		txData          map[string]any
		txHash, wStatus string
	)
	if status == "success" || status == "BlockchainConfirmed" || item.Currency == "UMBRELLA" {
		// Get chain service instance from registry
		chainService, err := chains.GetChainService(item.Network)
		if err != nil {
			return false, err
		}

		// Send transaction from master wallet to address from withdraw request
		txHash, err = chainService.SendTransaction("master", nil, item.Currency, item.To, item.Amount, item.Memo)
		if err != nil {
			return false, err
		}

		// Set transaction item with success status
		wStatus = "success"
		txData = map[string]any{
			"bybit_id": item.BybitId,
			"status":   wStatus,
			"hash":     txHash,
			"aml_id":   item.AmlId,
		}

	} else {
		// Set transaction item with failed status
		wStatus = "failed"
		txData = map[string]any{
			"bybit_id": item.BybitId,
			"status":   wStatus,
		}
	}

	txItem, err := json.Marshal(txData)
	if err != nil {
		return false, fmt.Errorf("failed to marshal bybit withdrawal tx item: %w", err)
	}

	// Add transaction item in withdrawals Redis HASH
	t.redis.HSet(ctx, t.RedisWithdrawalsKey, requestId, txItem)

	// Register withdrawal action in DB
	_, err = actionRepo.Create(&models.Action{
		UserId:   item.UserId,
		Type:     "withdrawal",
		Network:  item.Network,
		Currency: item.Currency,
		Amount:   item.Amount,
		Hash:     txHash,
		Status:   wStatus,
	})
	if err != nil {
		return false, fmt.Errorf("failed to create withdrawal action in network %s for user %d: %w",
			item.Network, item.UserId, err)
	}

	log.Printf("[WithdrawalsTask] Process item successfully: requestId=%s itemStr=%s", requestId, itemStr)
	return false, nil
}
