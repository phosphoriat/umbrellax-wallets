package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/services/chains"
)

type HashReader struct {
	bybit               *HTTPClient
	redis               *redis.Client
	interval            time.Duration
	ctx                 context.Context
	cancel              context.CancelFunc
	RedisBybitReqsKey   string
	RedisWithdrawalsKey string
}

// NewHashReader Create new HashReader instance
func NewHashReader(cfg *config.Config) *HashReader {
	ctx, cancel := context.WithCancel(context.Background())

	return &HashReader{
		bybit:               GetHTTPClient(),
		redis:               redis.GetClient(),
		interval:            10 * time.Second,
		ctx:                 ctx,
		cancel:              cancel,
		RedisBybitReqsKey:   cfg.RedisBybitReqsKey,
		RedisWithdrawalsKey: cfg.RedisWithdrawalsKey,
	}
}

// InitWithdrawalsRead Initialize reading from Redis
func InitWithdrawalsRead(cfg *config.Config) {
	hashReader := NewHashReader(cfg)
	go hashReader.RunChannelRead()
}

// RunChannelRead Start read bybit withdrawals requests from Redis every 10 sec
func (hr *HashReader) RunChannelRead() {
	// Buffered queue channel
	taskChan := make(chan struct{}, 1000)

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
	fmt.Printf("Start reading from Redis %s hash...", hr.RedisBybitReqsKey)
	for range taskChan {
		hr.readHash()
	}
}

// Stop reading from Redis
func (hr *HashReader) Stop() {
	hr.cancel()
}

// @private readHash Read new entities from Redis hash
func (hr *HashReader) readHash() {
	// Read all hash
	result, err := redis.GetClient().HGetAll(hr.ctx, hr.RedisBybitReqsKey)
	if err != nil {
		log.Printf("Read hash error '%s': %v", hr.RedisBybitReqsKey, err)
		return
	}

	// Process every item from hash
	for field, value := range result {
		err := hr.processItem(field, value)
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
		return fmt.Errorf("failed to unmarshal bybit withdraw request item: %w", err)
	}

	// Check types with validation
	currency, ok := item["currency"].(string)
	if !ok {
		return fmt.Errorf("invalid coin value")
	}
	network, ok := item["network"].(string)
	if !ok {
		return fmt.Errorf("invalid network value")
	}
	to, ok := item["to"].(float64)
	if !ok {
		return fmt.Errorf("invalid amount value")
	}
	amount, ok := item["amount"].(float64)
	if !ok {
		return fmt.Errorf("invalid amount value")
	}

	// Send withdraw from bybit to master wallet
	bybitId, err := hr.bybit.CreateWithdraw(currency, network, amount)
	if err != nil {
		// Delete item from requests hash
		hr.redis.HDel(hr.ctx, hr.RedisBybitReqsKey, requestId)

		// Add item in withdrawals hash with failed status
		wItem, _ := json.Marshal(map[string]any{
			"bybit_id": bybitId,
			"status":   "failed",
		})
		hr.redis.HSet(hr.ctx, hr.RedisWithdrawalsKey, requestId, wItem)

		return fmt.Errorf("request error: %w", err)
	}

	// Add item in withdrawals hash with pending status
	wItem, _ := json.Marshal(map[string]any{
		"status": "pending",
	})
	hr.redis.HSet(hr.ctx, hr.RedisWithdrawalsKey, requestId, wItem)

	// Get chain service instance from registry
	chainService, err := chains.GetChainService(network)
	if err != nil {
		return err
	}

	// @TODO Send chain withdraw from master wallet to address from withdraw request
	tx, err := chainService.SendTransaction(currency, to, amount)

	// Delete item from requests hash
	hr.redis.HDel(hr.ctx, hr.RedisBybitReqsKey, requestId)

	return nil
}
