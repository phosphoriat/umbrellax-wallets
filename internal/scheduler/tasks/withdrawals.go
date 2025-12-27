package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/redis"
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
	// Get all items from hash
	values, err := t.redis.HGetAll(ctx, t.RedisBybitWithdrawalsKey)
	if err != nil {
		return fmt.Errorf("withdrawals hash read error '%s': %w", t.RedisBybitWithdrawalsKey, err)
	}

	// Process all items from hash
	for key, value := range values {
		err = t.processItem(ctx, key, value)
		if err != nil {
			log.Printf("Withdrawals item processing error: %v", err)
		}

		// Delete item from bybit withdrawals Redis hash
		t.redis.HDel(ctx, t.RedisBybitWithdrawalsKey, key)
	}

	return nil
}

// @private processItem Process hash item
func (t *WithdrawalsTask) processItem(ctx context.Context, requestId, itemStr string) error {
	// Parse JSON item string
	var item chains.WithdrawalTransaction
	if err := json.Unmarshal([]byte(itemStr), &item); err != nil {
		return fmt.Errorf("failed to unmarshal bybit withdrawal tx item: %w", err)
	}
	if item.BybitId == "" || item.Currency == "" || item.Network == "" || item.To == "" || item.Amount == 0 {
		return fmt.Errorf("invalid bybit withdrawal tx item data: %+v", item)
	}

	// Check bybit withdrawal tx status
	resultMap, err := t.bybit.GetWithdrawalRecords(ctx, map[string]any{"withdrawID": item.BybitId})
	if err != nil {
		return err
	}

	if status, ok := utils.DataGet(resultMap, "0.status"); ok {
		var txData map[string]any

		// If status is success
		if status == "success" {
			// Get chain service instance from registry
			chainService, err := chains.GetChainService(item.Network)
			if err != nil {
				return err
			}

			// Send transaction from master wallet to address from withdraw request
			hash, err := chainService.SendTransaction("master", nil, item.Currency, item.To, item.Amount, item.Memo)
			if err != nil {
				return err
			}

			// Set transaction item with pending status
			txData = map[string]any{
				"bybit_id": item.BybitId,
				"status":   "pending",
				"hash":     hash,
				"aml_id":   item.AmlId,
			}

		} else {
			// Set transaction item with failed status
			txData = map[string]any{
				"bybit_id": item.BybitId,
				"status":   "failed",
			}
		}

		txItem, err := json.Marshal(txData)
		if err != nil {
			return fmt.Errorf("failed to marshal bybit withdrawal tx item: %w", err)
		}

		// Add transaction item in withdrawals Redis HASH
		t.redis.HSet(ctx, t.RedisWithdrawalsKey, requestId, txItem)

		return nil
	}
	return fmt.Errorf("bybit withdrawal tx status parse error")
}
