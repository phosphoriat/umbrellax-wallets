package chains

import (
	"context"
	"encoding/json"
	"fmt"

	"wallets/internal/config"
	"wallets/internal/database/redis"

	"github.com/google/uuid"
)

type (
	// BaseChainService Common functionality for all chains
	BaseChainService struct {
		Code string
	}

	// ChainService Interface with all chain methods
	ChainService interface {
		GetCode() string
		GetUserWallet(userId int, prefix *string) (string, error)
		GetMasterWallet() (map[string]any, error)
		GetTransactionCommission(currency, to string, amount float64) (map[string]any, error)
		GetTransactionStatus(hash string) (string, error)
		SendTransaction(currency, to string, amount float64) (map[string]any, error)
		ProcessInboundTransactions()
	}
)

// CreateBybitWithdrawRequest Create withdraw request to bybit
func CreateBybitWithdrawRequest(currency, network, address string, amount float64) (map[string]any, error) {
	// Params for withdraw request to master wallet (step 1)
	params := map[string]any{
		"currency": currency,
		"network":  network,
		"address":  address,
		"amount":   amount,
	}

	reqItem, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal withdraw request item: %w", err)
	}

	// Load config
	cfg := config.Load()
	// Generate unique request id
	requestId := uuid.New().String()
	// Add withdraw request item to Redis bybit withdrawal requests hash
	redis.GetClient().HSet(context.Background(), cfg.RedisBybitReqsKey, requestId, string(reqItem))

	return map[string]any{
		"queue_id": requestId,
	}, nil
}
