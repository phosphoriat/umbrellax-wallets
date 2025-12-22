package chains

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/repository"
	"wallets/internal/services/api/aml"

	"github.com/avast/retry-go/v5"
	"github.com/google/uuid"
)

type (
	// InboundTransaction Inbound transaction struct
	InboundTransaction struct {
		UserId   int     `json:"user_id"`
		Currency string  `json:"currency"`
		TxId     string  `json:"tx_id"`
		From     string  `json:"from"`
		To       string  `json:"to"`
		Amount   float64 `json:"amount"`
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
		GetUserWallet(userId int) (string, error)
		GetMasterWallet() (map[string]any, error)
		GetTransactionCommission(oType, wType string, wId *string, currency, to string, amount float64, memo string) (map[string]float64, error)
		GetTransactionStatus(hash, toAddr string) (string, error)
		SendTransaction(wType string, wId *string, currency, to string, amount float64, memo string) (string, error)
		GetInboundTransactions() ([]*InboundTransaction, error)
	}
)

// ProcessInboundTransactions Process inbound transactions
func ProcessInboundTransactions(ctx context.Context) error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("[ProcessInboundTransactions] Failed to load config: %w", err)
	}

	// Wait group to wait for all network goroutines to complete
	var wg sync.WaitGroup

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

		// Process every network in separate goroutine
		go func() {
			defer wg.Done()

			// Get inbound transactions list
			txList, err := chainService.GetInboundTransactions()
			if err != nil {
				log.Printf("[ProcessInboundTransactions] [%s] Failed to get transactions list: %v", network, err)
				return
			}
			if len(txList) == 0 {
				log.Printf("[ProcessInboundTransactions] [%s] No transactions found", network)
			}

			// Timeout for network processing (60 seconds for all transactions)
			netCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()

			// Process inbound transactions list
			for _, tx := range txList {
				// Check context for cancellation
				select {
				case <-netCtx.Done():
					log.Printf("[ProcessInboundTransactions] [%s] Context cancelled: %v", network, netCtx.Err())
					return
				default:
				}

				// Refill transaction AML check
				amlId, err := aml.GetClient().CheckTransaction(network, tx.Currency, tx.TxId, tx.To, "incoming")
				if err != nil {
					log.Printf("[ProcessInboundTransactions] [%s] AML check incoming tx '%s' failed: %v",
						network, tx.TxId, err)
					// Continue processing, if AML check failed
				}

				// Transfer tokens from user wallet to master wallet and return transfer fee (in native network token)
				transferFeeAmount, err := TransferTokensToMasterWallet(netCtx, chainService, strconv.Itoa(tx.UserId), tx.Currency, tx.To, tx.Amount)
				if err != nil {
					log.Printf("[ProcessInboundTransactions] [%s] Failed to transfer tokens to master wallet: %v",
						network, err)
					// Continue processing, if transfer tokens failed
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
					log.Printf("[ProcessInboundTransactions] [%s] Successfully processed tx '%s'",
						network, tx.TxId)
				}
			}
		}()
	}

	// Waiting for all network goroutines
	wg.Wait()

	return nil
}

// TransferTokensToMasterWallet Transfer tokens from user wallet to master wallet (with retrying). In current version at Bybit wallet
// ctx: Current process context
// chainService: Network service instance
// userId: User id
// currency: Token ticker
// userAddr: User wallet address
// amount: Amount in float value
// Returns: Transfer fee amount (in native network token value)
func TransferTokensToMasterWallet(ctx context.Context, chainService ChainService, userId, currency, userAddr string, amount float64) (float64, error) {
	network := chainService.GetCode()

	var bybitAddr, nativeToken string
	var transferFeeAmount, masterFeeAmount, feeBuffer float64

	// Create retrier with options
	retrier := retry.New(
		retry.Attempts(3),
		retry.Delay(2*time.Second),
		retry.DelayType(retry.FixedDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Printf("[TransferTokensToMasterWallet] retry attempt #%d: %v", n, err)
		}),
		retry.Context(ctx),
	)

	// Get network data from config
	err := retrier.Do(
		func() error {
			var err error
			bybitAddr, err = config.GetChainSetting[string](
				config.NetworkSettings,
				fmt.Sprintf("%s.bybit_address", network),
			)
			if err != nil {
				return fmt.Errorf("failed to get bybit address for %s network: %w",
					network, err)
			}

			nativeToken, err = config.GetChainSetting[string](
				config.NetworkSettings,
				fmt.Sprintf("%s.native_token", network),
			)
			if err != nil {
				return fmt.Errorf("failed to get native token for %s network: %w",
					network, err)
			}

			feeBuffer, err = config.GetChainSetting[float64](
				config.NetworkSettings,
				fmt.Sprintf("%s.fee_buffer", network),
			)
			if err != nil {
				return fmt.Errorf("failed to get fee buffer for %s network: %w",
					network, err)
			}

			return nil
		},
	)
	if err != nil {
		return 0, fmt.Errorf("[TransferTokensToMasterWallet] GetMasterWalletData step failed: %w", err)
	}

	// Calc needed fee amount (in native network token) for sending tokens from user to master wallet
	err = retrier.Do(
		func() error {
			// Calc fee amount for user -> bybit transfer
			userFee, err := chainService.GetTransactionCommission("inner", "user", &userId, currency, bybitAddr, amount, "")
			if err != nil {
				return fmt.Errorf("failed to get transaction commission: %w", err)
			}
			// Add fee buffer
			transferFeeAmount = userFee["chain_fee"] + userFee["chain_fee"]*feeBuffer

			// Calc fee amount for master -> user wallet native token transfer
			masterFee, err := chainService.GetTransactionCommission("inner", "master", &userId, currency, userAddr, amount, "")
			if err != nil {
				return fmt.Errorf("failed to get transaction commission: %w", err)
			}
			masterFeeAmount = masterFee["chain_fee"]

			return nil
		},
	)
	if err != nil {
		return 0, fmt.Errorf("[TransferTokensToMasterWallet] GetTransactionCommission step failed: %w", err)
	}

	// Send native token for fee payment from master to user wallet, if currency != native token
	if currency != nativeToken {
		err = retrier.Do(
			func() error {
				// Send transaction (master -> user)
				hash, err := chainService.SendTransaction("master", nil, nativeToken, userAddr, transferFeeAmount, "")
				if err != nil {
					return fmt.Errorf("failed to send native token from master to user: %w", err)
				}

				log.Printf("%s token sent from master to user wallet, hash: '%s'", nativeToken, hash)
				return nil
			},
		)
		if err != nil {
			return 0, fmt.Errorf("[TransferTokensToMasterWallet] SendNativeTokenToUser step failed: %w", err)
		}

		// Slight delay to confirm native token transaction (2 seconds)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return 0, fmt.Errorf("[TransferTokensToMasterWallet] Context cancelled during delay: %w", ctx.Err())
		}
	}

	// Send tokens from user to Bybit wallet
	err = retrier.Do(
		func() error {
			// If currency = nativeToken, subtract commission from amount
			if currency != nativeToken {
				amount = amount - transferFeeAmount
			}

			// Send transaction (user -> bybit)
			uHash, err := chainService.SendTransaction("user", &userId, currency, bybitAddr, amount, "")
			if err != nil {
				return fmt.Errorf("failed to send tokens from user to master: %w", err)
			}

			log.Printf("%s token transfer from user %s to master, hash: '%s'", currency, userId, uHash)
			return nil
		},
	)
	if err != nil {
		return 0, fmt.Errorf("[TransferTokensToMasterWallet] SendTokensToMaster step failed: %w", err)
	}

	// Return master -> user and user -> bybit total transfers commission
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
