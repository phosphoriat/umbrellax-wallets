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
		log.Printf("Read hash error '%s': %v", t.RedisBybitWithdrawalsKey, err)
		return nil
	}

	// Process all items from hash
	for key, value := range values {
		err = t.processItem(ctx, key, value)
		if err != nil {
			log.Printf("Process item error: %v", err)
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

	// Check types with validation
	bybitId, ok := item["bybit_id"].(string)
	if !ok {
		return fmt.Errorf("bybit withdrawal tx item: invalid bybit_id value")
	}
	currency, ok := item["currency"].(string)
	if !ok {
		return fmt.Errorf("bybit withdrawal tx item: invalid coin value")
	}
	network, ok := item["network"].(string)
	if !ok {
		return fmt.Errorf("bybit withdrawal tx item: invalid network value")
	}
	to, ok := item["to"].(string)
	if !ok {
		return fmt.Errorf("bybit withdrawal tx item: invalid amount value")
	}
	amount, ok := item["amount"].(float64)
	if !ok {
		return fmt.Errorf("bybit withdrawal tx item: invalid amount value")
	}
	memo, ok := item["memo"].(string)
	if !ok {
		memo = ""
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

			// Send chain withdraw from master wallet to address from withdraw request
			tx, err := chainService.SendTransaction(currency, to, amount, memo)
			if err != nil {
				return err
			}

			// Add transaction item in withdrawals Redis hash with pending status
			txItem, _ = json.Marshal(map[string]any{
				"bybit_id": bybitId,
				"status":   "pending",
				"hash":     tx["hash"].(string),
				"aml_id":   tx["aml_id"].(string),
			})

		} else {
			// Add transaction item in withdrawals Redis hash with pending status
			txItem, _ = json.Marshal(map[string]any{
				"bybit_id": bybitId,
				"status":   "failed",
			})
		}

		// Add transaction item in withdrawals Redis hash
		t.redis.HSet(ctx, t.RedisWithdrawalsKey, requestId, txItem)

	}
	return fmt.Errorf("parse bybit withdrawal tx status: %w", err)
}
