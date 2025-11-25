package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

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
			name:        "withdrawals",
			description: "Process withdrawals",
		},
		bybit:                    bybit.GetHTTPClient(),
		redis:                    redis.GetClient(),
		RedisBybitWithdrawalsKey: cfg.RedisBybitWithdrawalsKey,
		RedisWithdrawalsKey:      cfg.RedisWithdrawalsKey,
	}
}

func (t *WithdrawalsTask) Execute(ctx context.Context) error {
	// Get all items from hash
	values, err := t.redis.HGetAll(ctx, t.RedisBybitWithdrawalsKey)
	if err != nil {
		log.Printf("Withdrawals hash read error '%s': %v", t.RedisBybitWithdrawalsKey, err)
		return nil
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
	// Unmarshalling json item string
	var item map[string]any
	err := json.Unmarshal([]byte(itemStr), &item)
	if err != nil {
		return fmt.Errorf("failed to unmarshal bybit withdrawal tx item: %w", err)
	}

	// Check types assertion
	bybitId, okB := item["bybit_id"].(string)
	currency, okC := item["currency"].(string)
	network, okN := item["network"].(string)
	to, okT := item["to"].(string)
	amount, okA := item["amount"].(float64)
	memo, okM := item["memo"].(string)
	if !okM {
		memo = ""
	}
	if !okB || !okC || !okN || !okT || !okA {
		return fmt.Errorf("invalid bybit withdrawal tx item data: %+v", item)
	}

	// Check bybit withdrawal tx status
	resultMap, err := t.bybit.GetWithdrawalRecords(map[string]any{"withdrawID": bybitId})
	if err != nil {
		return err
	}

	if status, ok := utils.DataGet(resultMap, "0.status"); ok {
		var txItem []byte

		// If status is success
		if status == "success" {
			// Get chain service instance from registry
			chainService, err := chains.GetChainService(network)
			if err != nil {
				return err
			}

			// Send transaction from master wallet to address from withdraw request
			hash, amlId, err := chainService.SendTransaction("master", nil, currency, to, amount, memo)
			if err != nil {
				return err
			}

			// Set transaction item with pending status
			txItem, _ = json.Marshal(map[string]any{
				"bybit_id": bybitId,
				"status":   "pending",
				"hash":     hash,
				"aml_id":   amlId,
			})

		} else {
			// Set transaction item with failed status
			txItem, _ = json.Marshal(map[string]any{
				"bybit_id": bybitId,
				"status":   "failed",
			})
		}

		// Push transaction item in withdrawals Redis queue
		t.redis.HSet(ctx, t.RedisWithdrawalsKey, requestId, txItem)

		return nil

	} else {
		return fmt.Errorf("bybit withdrawal tx status parse error: %w", err)
	}
}
