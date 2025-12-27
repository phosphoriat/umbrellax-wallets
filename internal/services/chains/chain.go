package chains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/repository"
	"wallets/internal/services/api/aml"
	"wallets/internal/services/api/rates"

	"github.com/avast/retry-go/v5"
	"github.com/google/uuid"
)

type (
	// InboundTransaction Inbound transaction struct
	InboundTransaction struct {
		UserId   uint    `json:"user_id"`
		Currency string  `json:"currency"`
		TxId     string  `json:"tx_id"`
		From     string  `json:"from"`
		To       string  `json:"to"`
		Amount   float64 `json:"amount"`
	}

	// WithdrawalTransaction Withdrawal transaction struct
	WithdrawalTransaction struct {
		BybitId  string  `json:"bybit_id,omitempty"`
		Status   string  `json:"status,omitempty"`
		Currency string  `json:"currency"`
		Network  string  `json:"network"`
		To       string  `json:"to"`
		Amount   float64 `json:"amount"`
		Memo     string  `json:"memo,omitempty"`
		AmlId    string  `json:"aml_id,omitempty"`
	}

	// BaseChainService Common fields struct for all chains
	BaseChainService struct {
		WalletRepo  *repository.WalletRepository
		UserRepo    *repository.UserRepository
		NetworkCode string
		NativeToken string
	}

	// ChainService Chain interface with all methods
	ChainService interface {
		GetCode() string
		GetUserWallet(userId uint32) (string, error)
		GetMasterWallet() (map[string]any, error)
		GetTransactionCommission(oType, wType string, wId *uint32, currency, to string, amount float64, memo string) (map[string]float64, error)
		GetTransactionStatus(hash, toAddr string) (string, error)
		SendTransaction(wType string, wId *uint32, currency, to string, amount float64, memo string) (string, error)
		GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error)
	}
)

// ProcessInboundTransactions Process inbound transactions (scheduled every 10 sec)
// ctx: Process context from scheduler
func ProcessInboundTransactions(ctx context.Context) error {
	// Load config
	cfg, _ := config.Load()

	// Wait group to wait for all network goroutines to complete
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errorsList []error

	// Get all networks list
	networks := GetAvailableNetworks()

	for _, network := range networks {
		// Get chain service instance for network
		chainService, err := GetChainService(network)
		if err != nil {
			log.Printf("[ProcessInboundTransactions] [%s] Failed to get network chain service: %v", network, err)
			continue
		}

		wg.Add(1)

		// Process every network in separate goroutines
		go func() {
			defer wg.Done()

			// Get inbound transactions list
			txList, err := chainService.GetInboundTransactions(ctx)
			if err != nil {
				log.Printf("[ProcessInboundTransactions] %v", err)

				mu.Lock()
				errorsList = append(errorsList, fmt.Errorf("[%s] %w", network, err))
				mu.Unlock()

				return
			}

			if len(txList) > 0 {
				log.Printf("[ProcessInboundTransactions] [%s] Start processing [%d] user transactions",
					network, len(txList))
			}

			// Process inbound transactions list
			for _, tx := range txList {
				var amlId string

				// Get tx currency rate from cache
				if rate, ok := rates.GetClient().GetRate("crypto", tx.Currency, "USD"); ok {
					// Get tx amount in USD
					amountUsd := tx.Amount * rate

					// Refill transaction AML check, if tx amount in USD equals or higher aml threshold amount
					if amountUsd >= cfg.AmlThreshold {
						amlId, err = aml.GetClient().CheckTransaction(ctx, network, tx.Currency, tx.TxId, tx.To, "incoming")
						if err != nil {
							log.Printf("[ProcessInboundTransactions] [%s] AML check incoming tx '%s' failed: %v",
								network, tx.TxId, err)
							// Continue processing, even if AML check failed
						}
					}
				}

				// Transfer tokens from user wallet to master wallet and return transfer fee (in native network token)
				transferFeeAmount, err := TransferTokensToMasterWallet(
					ctx, chainService, uint32(tx.UserId), tx.Currency, tx.To, tx.Amount,
				)
				if err != nil {
					log.Printf("[ProcessInboundTransactions] [%s] Failed to transfer tokens to master wallet: %v",
						network, err)
					// Continue processing, even if transfer tokens failed
				}

				// Get refill tx item encoded in JSON
				txItem, err := json.Marshal(map[string]any{
					"user_id":  tx.UserId,
					"currency": tx.Currency,
					"network":  network,
					"hash":     tx.TxId,
					"from":     tx.From,
					"to":       tx.To,
					"amount":   tx.Amount,
					"fee":      transferFeeAmount,
					"aml_id":   amlId,
				})
				if err != nil {
					log.Printf("[ProcessInboundTransactions] [%s] Failed to marshal tx item for tx %s: %v",
						network, tx.TxId, err)
					continue
				}

				// Push item in Redis refills queue
				err = redis.GetClient().HSet(context.Background(), cfg.RedisRefillsKey, tx.TxId, string(txItem))
				if err != nil {
					log.Printf("[ProcessInboundTransactions] [%s] Failed to push tx '%s' to Redis: %v",
						network, tx.TxId, err)
				} else {
					log.Printf("[ProcessInboundTransactions] [%s] Tx '%s' successfully processed",
						network, tx.TxId)
				}
			}
		}()
	}

	// Waiting for all network goroutines
	wg.Wait()

	if len(errorsList) > 0 {
		return errors.Join(errorsList...)
	}
	return nil
}

// TransferTokensToMasterWallet Transfer tokens from user→master wallet (with retrying)
// @note In current version at Bybit wallet
// ctx: Current process context
// chainService: Network service instance
// userId: User id
// currency: Token ticker
// userAddr: User wallet address
// amount: Amount in float value
// Returns: Transfer fee amount (in native network token value)
func TransferTokensToMasterWallet(ctx context.Context, chainService ChainService, userId uint32, currency, userAddr string, amount float64) (float64, error) {
	// Get network data from config
	network := chainService.GetCode()
	cfg, ok := config.GetNetworkConfig(network)
	if !ok {
		return 0, fmt.Errorf("[TransferTokensToMasterWallet] Unknown network: %s", network)
	}

	// Create retrier entity
	retrier := retry.New(
		retry.Attempts(3),
		retry.Delay(5*time.Second),
		retry.DelayType(retry.FixedDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Printf("[TransferTokensToMasterWallet] retry attempt #%d: %v", n, err)
		}),
		retry.Context(ctx),
	)

	var (
		retryErr                           error
		transferFeeAmount, masterFeeAmount float64
	)

	// Calc needed fee amount (in native network token) for sending tokens from user to Bybit wallet
	retryErr = retrier.Do(
		func() error {
			// Calc fee amount for user→bybit transfer
			userFee, err := chainService.GetTransactionCommission("inner", "user", &userId, currency, cfg.BybitAddress, amount, "")
			if err != nil {
				return fmt.Errorf("failed to get transaction commission: %w", err)
			}
			// Add fee buffer
			transferFeeAmount = userFee["chain_fee"] + userFee["chain_fee"]*cfg.FeeBuffer

			// Calc fee amount for master→user wallet native token transfer
			masterFee, err := chainService.GetTransactionCommission("inner", "master", &userId, currency, userAddr, amount, "")
			if err != nil {
				return fmt.Errorf("failed to get transaction commission: %w", err)
			}
			masterFeeAmount = masterFee["chain_fee"]

			return nil
		},
	)
	if retryErr != nil {
		return 0, fmt.Errorf("[TransferTokensToMasterWallet] GetTransactionCommission step failed: %w", retryErr)
	}

	// Send native token for fee payment from master to user wallet, if currency != native token
	if currency != cfg.NativeToken {
		retryErr = retrier.Do(
			func() error {
				// Send transaction (master→user)
				hash, err := chainService.SendTransaction("master", nil, cfg.NativeToken, userAddr, transferFeeAmount, "")
				if err != nil {
					return fmt.Errorf("failed to send native token from master to user: %w", err)
				}

				log.Printf("%s token sent from master to user wallet, hash: '%s'", cfg.NativeToken, hash)
				return nil
			},
		)
		if retryErr != nil {
			return 0, fmt.Errorf("[TransferTokensToMasterWallet] SendNativeTokenToUser step failed: %w", retryErr)
		}

		// Delay to wait for confirm native token transaction (3 seconds)
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return 0, fmt.Errorf("[TransferTokensToMasterWallet] Context cancelled during delay: %w", ctx.Err())
		}
	}

	// Send tokens from user to Bybit wallet
	retryErr = retrier.Do(
		func() error {
			// If currency = nativeToken, subtract commission from amount
			if currency != cfg.NativeToken {
				amount = amount - transferFeeAmount
			}

			// Send transaction (user→bybit)
			uHash, err := chainService.SendTransaction("user", &userId, currency, cfg.BybitAddress, amount, "")
			if err != nil {
				return fmt.Errorf("failed to send tokens from user to master: %w", err)
			}

			log.Printf("%s token transfer from user %s to master, hash: '%s'", currency, userId, uHash)
			return nil
		},
	)
	if retryErr != nil {
		return 0, fmt.Errorf("[TransferTokensToMasterWallet] SendTokensToMaster step failed: %w", retryErr)
	}

	// Return master→user and user→bybit total transfers commission
	return transferFeeAmount + masterFeeAmount, nil
}

// CreateWithdrawRequest Put new withdraw request in the queue and return queue_id
// currency: Token ticker
// network: Network code
// address: Withdrawal address
// amount: Amount in float value
// memo: Memo tag (for TON only)
// amlId: AML address check id
// Returns: Withdraw request queue id
func CreateWithdrawRequest(currency, network, address string, amount float64, memo, amlId string) (map[string]any, error) {
	// Load config
	cfg, _ := config.Load()

	// Get withdraw request item encoded in JSON
	reqItem, err := json.Marshal(map[string]any{
		"currency": currency,
		"network":  network,
		"address":  address,
		"amount":   amount,
		"memo":     memo,
		"aml_id":   amlId,
	})
	if err != nil {
		return nil, fmt.Errorf("[CreateWithdrawRequest] Failed to marshal withdraw request item: %w", err)
	}

	// Generate unique request id
	requestId := uuid.New().String()
	// Add withdraw request item to Redis bybit withdrawal requests hash
	redis.GetClient().HSet(context.Background(), cfg.RedisBybitReqsKey, requestId, string(reqItem))

	return map[string]any{
		"queue_id": requestId,
	}, nil
}
