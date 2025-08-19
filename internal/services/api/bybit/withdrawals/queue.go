package withdrawals

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

type HashReader struct {
	bybit                    *bybit.HTTPClient
	redis                    *redis.Client
	interval                 time.Duration
	ctx                      context.Context
	cancel                   context.CancelFunc
	RedisBybitWithdrawalsKey string
	RedisWithdrawalsKey      string
}

// NewHashReader Create new HashReader instance
func NewHashReader(cfg *config.Config) *HashReader {
	ctx, cancel := context.WithCancel(context.Background())

	return &HashReader{
		bybit:                    bybit.GetHTTPClient(),
		redis:                    redis.GetClient(),
		interval:                 10 * time.Second,
		ctx:                      ctx,
		cancel:                   cancel,
		RedisBybitWithdrawalsKey: cfg.RedisBybitWithdrawalsKey,
		RedisWithdrawalsKey:      cfg.RedisWithdrawalsKey,
	}
}

// InitWithdrawalsRead Initialize reading from Redis bybit withdrawals hash
func InitWithdrawalsRead(cfg *config.Config) {
	hashReader := NewHashReader(cfg)
	go hashReader.RunChannelRead()
}

// RunChannelRead Start read bybit withdrawals requests from Redis every 10 sec
func (hr *HashReader) RunChannelRead() {
	// Queue channel
	taskChan := make(chan struct{})

	// Goroutine for sending tasks every 10 seconds
	go func() {
		ticker := time.NewTicker(hr.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				select {
				case taskChan <- struct{}{}:
					// Task added in queue
				default:
					fmt.Println("The queue is full, skip task")
				}
			case <-hr.ctx.Done():
				close(taskChan)
				return
			}
		}
	}()

	// Process tasks from queue
	for range taskChan {
		hr.readHash()
	}
}

// Stop reading from Redis
func (hr *HashReader) Stop() {
	hr.cancel()
}

// @private readHash Read Redis hash
func (hr *HashReader) readHash() {
	// Get all items from hash
	values, err := redis.GetClient().HGetAll(hr.ctx, hr.RedisBybitWithdrawalsKey)
	if err != nil {
		log.Printf("Read hash error '%s': %v", hr.RedisBybitWithdrawalsKey, err)
		return
	}

	// Process all items from hash
	for field, value := range values {
		err = hr.processItem(field, value)
		if err != nil {
			return
		}
	}
}

// @private processItem Process Redis hash item
func (hr *HashReader) processItem(requestId, itemJson string) error {
	// Unmarshalling json item string
	var item map[string]any
	err := json.Unmarshal([]byte(itemJson), &item)
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

	// Check bybit withdrawal tx status
	result, err := bybit.GetHTTPClient().GetWithdrawalRecords(map[string]any{
		"withdrawID": bybitId,
	})
	if err != nil {
		return err
	}

	if row, ok := result[0].(map[string]any); ok {
		if status, ok := row["status"].(string); ok {
			var txItem []byte

			// If status is success
			if status == "success" {
				// Get chain service instance from registry
				chainService, err := chains.GetChainService(network)
				if err != nil {
					return err
				}

				// Send chain withdraw from master wallet to address from withdraw request
				tx, err := chainService.SendTransaction(currency, to, amount)
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
			hr.redis.HSet(hr.ctx, hr.RedisWithdrawalsKey, requestId, txItem)

			// Delete item from bybit withdrawals Redis hash
			hr.redis.HDel(hr.ctx, hr.RedisBybitWithdrawalsKey, requestId)

		} else {
			return fmt.Errorf("parse bybit withdrawal tx status: invalid response format")
		}
	} else {
		return fmt.Errorf("parse bybit withdrawal tx status: invalid response format")
	}

	return nil
}
