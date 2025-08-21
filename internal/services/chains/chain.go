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
	// BaseChainService Common fields struct for all chains
	BaseChainService struct {
		NetworkCode string
		NativeToken string
		AmlTokenId  string
	}

	// ChainService Interface with all chain methods
	ChainService interface {
		GetCode() string
		GetUserWallet(userId int) (string, error)
		GetMasterWallet() (map[string]any, error)
		GetTransactionCommission(currency, to string, amount float64) (map[string]any, error)
		GetTransactionStatus(hash string) (string, error)
		SendTransaction(currency, to string, amount float64) (map[string]any, error)
		ProcessInboundTransactions()
	}
)

// CreateWithdrawRequest Put new withdraw request in the queue and return queue_id
// currency: Token ticker
// network: Network code
// address: Withdrawal address
// amount: Amount in float value
func CreateWithdrawRequest(currency, network, address string, amount float64) (map[string]any, error) {
	// Load config
	cfg := config.Load()

	// Get withdraw request to master wallet item encoded in JSON
	reqItem, err := json.Marshal(map[string]any{
		"currency": currency,
		"network":  network,
		"address":  address,
		"amount":   amount,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal withdraw request item: %w", err)
	}

	// Generate unique request id
	requestId := uuid.New().String()
	// Add withdraw request item to Redis bybit withdrawal requests hash
	redis.GetClient().HSet(context.Background(), cfg.RedisBybitReqsKey, requestId, string(reqItem))

	return map[string]any{
		"queue_id": requestId,
	}, nil
}
