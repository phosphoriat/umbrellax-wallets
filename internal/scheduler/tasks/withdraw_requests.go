package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/services/api/bybit"
)

type WithdrawRequestTask struct {
	BaseTask
	bybit                    *bybit.HTTPClient
	redis                    *redis.Client
	RedisBybitReqsKey        string
	RedisBybitWithdrawalsKey string
	RedisWithdrawalsKey      string
}

func NewWithdrawRequestTask() *WithdrawRequestTask {
	cfg, _ := config.Load()

	return &WithdrawRequestTask{
		BaseTask: BaseTask{
			maxRetries:  3,
			name:        "refills",
			description: "Process withdraw requests",
		},
		bybit:                    bybit.GetHTTPClient(),
		redis:                    redis.GetClient(),
		RedisBybitReqsKey:        cfg.RedisBybitReqsKey,
		RedisBybitWithdrawalsKey: cfg.RedisBybitWithdrawalsKey,
		RedisWithdrawalsKey:      cfg.RedisWithdrawalsKey,
	}
}

func (t *WithdrawRequestTask) Execute(ctx context.Context) error {
	// Get first item from hash
	key, value, err := redis.GetClient().GetFirstHashElement(ctx, t.RedisBybitReqsKey)
	if err != nil {
		log.Printf("Read hash error '%s': %v", t.RedisBybitReqsKey, err)
		return nil
	}

	// Process first item from hash
	err = t.processItem(ctx, key, value)
	if err != nil {
		log.Printf("Process item error: %v", err)

		// Add item in withdrawals Redis hash with failed status
		txItem, _ := json.Marshal(map[string]any{
			"status": "failed",
		})
		t.redis.HSet(ctx, t.RedisWithdrawalsKey, key, txItem)
	}

	// Delete item from bybit requests Redis hash
	t.redis.HDel(ctx, t.RedisBybitReqsKey, key)

	return nil
}

// @private processItem Process hash item
func (t *WithdrawRequestTask) processItem(ctx context.Context, requestId, itemStr string) error {
	// Unmarshalling json item string
	var item map[string]any
	err := json.Unmarshal([]byte(itemStr), &item)
	if err != nil {
		return fmt.Errorf("failed to unmarshal bybit withdraw request item: %w", err)
	}

	// Check types assertion
	currency, okC := item["currency"].(string)
	network, okN := item["network"].(string)
	to, okT := item["to"].(string)
	amount, okA := item["amount"].(float64)
	memo, okM := item["memo"].(string)
	if !okM {
		memo = ""
	}
	if !okC || !okN || !okT || !okA {
		return fmt.Errorf("invalid bybit withdraw request item data: %+v", item)
	}

	// Send withdraw from bybit to master wallet
	bybitId, err := t.bybit.CreateWithdraw(currency, network, amount)
	if err != nil {
		return fmt.Errorf("bybit withdrawal tx error: %w", err)
	}

	// Add item in bybit_withdrawals Redis hash with success status
	wItem, _ := json.Marshal(map[string]any{
		"bybit_id": bybitId,
		"status":   "success",
		"currency": currency,
		"network":  network,
		"to":       to,
		"amount":   amount,
		"memo":     memo,
	})
	t.redis.HSet(ctx, t.RedisBybitWithdrawalsKey, requestId, wItem)

	return nil
}
