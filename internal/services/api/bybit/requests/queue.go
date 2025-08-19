package requests

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/services/api/bybit"
)

type HashReader struct {
	bybit                    *bybit.HTTPClient
	redis                    *redis.Client
	interval                 time.Duration
	ctx                      context.Context
	cancel                   context.CancelFunc
	RedisBybitReqsKey        string
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
		RedisBybitReqsKey:        cfg.RedisBybitReqsKey,
		RedisBybitWithdrawalsKey: cfg.RedisBybitWithdrawalsKey,
		RedisWithdrawalsKey:      cfg.RedisWithdrawalsKey,
	}
}

// InitRequestsRead Initialize reading from Redis withdrawals requests hash
func InitRequestsRead(cfg *config.Config) {
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
	// Get first item from hash
	key, value, err := redis.GetClient().GetFirstHashElement(hr.ctx, hr.RedisBybitReqsKey)
	if err != nil {
		log.Printf("Read hash error '%s': %v", hr.RedisBybitReqsKey, err)
		return
	}

	// Process item from hash
	err = hr.processItem(key, value)
	if err != nil {
		return
	}
}

// @private processItem Process Redis hash item
func (hr *HashReader) processItem(requestId, itemJson string) error {
	// Unmarshalling json item string
	var item map[string]any
	err := json.Unmarshal([]byte(itemJson), &item)
	if err != nil {
		return fmt.Errorf("failed to unmarshal bybit withdraw request item: %w", err)
	}

	// Check types with validation
	currency, ok := item["currency"].(string)
	if !ok {
		return fmt.Errorf("bybit withdraw request item: invalid coin value")
	}
	network, ok := item["network"].(string)
	if !ok {
		return fmt.Errorf("bybit withdraw request item: invalid network value")
	}
	to, ok := item["to"].(string)
	if !ok {
		return fmt.Errorf("bybit withdraw request item: invalid amount value")
	}
	amount, ok := item["amount"].(float64)
	if !ok {
		return fmt.Errorf("bybit withdraw request item: invalid amount value")
	}

	// Delete item from bybit requests Redis hash
	hr.redis.HDel(hr.ctx, hr.RedisBybitReqsKey, requestId)

	// Send withdraw from bybit to master wallet
	bybitId, err := hr.bybit.CreateWithdraw(currency, network, amount)
	if err != nil {
		// Add item in withdrawals Redis hash with failed status
		txItem, _ := json.Marshal(map[string]any{
			"status": "failed",
		})
		hr.redis.HSet(hr.ctx, hr.RedisWithdrawalsKey, requestId, txItem)

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
	})
	hr.redis.HSet(hr.ctx, hr.RedisBybitWithdrawalsKey, requestId, wItem)

	return nil
}
