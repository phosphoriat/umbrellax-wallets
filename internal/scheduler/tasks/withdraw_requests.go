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
)

type WithdrawRequestTask struct {
	BaseTask
	bybit                    *bybit.HTTPClient
	redis                    *redis.Client
	RedisBybitReqsKey        string
	RedisBybitWithdrawalsKey string
	RedisWithdrawalsKey      string
}

func NewWithdrawRequestsTask() *WithdrawRequestTask {
	cfg, _ := config.Load()

	return &WithdrawRequestTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			timeout:     30 * time.Second,
			name:        "withdraw_requests",
			description: "Process withdraw requests",
		},
		bybit:                    bybit.GetClient(),
		redis:                    redis.GetClient(),
		RedisBybitReqsKey:        cfg.RedisBybitReqsKey,
		RedisBybitWithdrawalsKey: cfg.RedisBybitWithdrawalsKey,
		RedisWithdrawalsKey:      cfg.RedisWithdrawalsKey,
	}
}

func (t *WithdrawRequestTask) Execute(ctx context.Context) error {
	// Get first item from hash
	key, value, err := t.redis.GetFirstHashElement(ctx, t.RedisBybitReqsKey)
	if err != nil {
		return fmt.Errorf("[WithdrawRequestTask] Read hash error '%s': %w", t.RedisBybitReqsKey, err)
	}
	if key == "" {
		return nil
	}

	// Process first item from hash
	err = t.processItem(ctx, key, value)
	if err != nil {
		// Add item in withdrawals Redis hash with failed status
		txItem, _ := json.Marshal(map[string]any{
			"status": "failed",
		})
		t.redis.HSet(ctx, t.RedisWithdrawalsKey, key, txItem)

		log.Printf("[WithdrawRequestTask] Process item error: %v", err)
	}

	// Delete item from bybit requests Redis hash
	t.redis.HDel(ctx, t.RedisBybitReqsKey, key)

	return nil
}

// @private processItem Process hash item
func (t *WithdrawRequestTask) processItem(ctx context.Context, requestId, itemStr string) error {
	// Parse JSON item string
	var item chains.WithdrawalTransaction
	if err := json.Unmarshal([]byte(itemStr), &item); err != nil {
		return fmt.Errorf("failed to unmarshal bybit withdrawal tx item: %w", err)
	}
	if item.Currency == "" || item.Network == "" || item.To == "" || item.Amount <= 0 {
		return fmt.Errorf("invalid bybit withdrawal tx item data: %+v", item)
	}

	var bybitId string
	if item.Currency != "UMBRELLA" {
		// Send withdraw from bybit to master wallet
		var err error
		bybitId, err = t.bybit.CreateWithdraw(ctx, item.Currency, item.Network, item.Amount)
		if err != nil {
			return fmt.Errorf("bybit withdrawal tx error: %w", err)
		}
	}

	// Add item in bybit_withdrawals Redis HASH with success status
	item.BybitId = bybitId
	item.Status = "success"
	wItem, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal bybit withdrawal item: %w", err)
	}

	t.redis.HSet(ctx, t.RedisBybitWithdrawalsKey, requestId, wItem)

	log.Printf("[WithdrawRequestTask] Process item successfully: requestId=%s bybitId=%s itemStr=%s",
		requestId, bybitId, itemStr)
	return nil
}
