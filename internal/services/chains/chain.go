package chains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
	"wallets/internal/services/api/bybit"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/models"
	"wallets/internal/repository"
	"wallets/internal/services/api/aml"
	"wallets/internal/services/api/notifications"
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
		UserId   uint    `json:"user_id,omitempty"`
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
		ActionRepo  *repository.ActionRepository
		NetworkCode string
		NativeToken string
	}

	// ChainService Chain interface with all methods
	ChainService interface {
		GetCode() string
		GetUserWalletAddress(userId uint32) (string, error)
		GetUserWallet(userId uint32) (map[string]any, error)
		GetUserWalletBalance(userId uint32, currency string) (float64, error)
		GetMasterWallet() (map[string]any, error)
		GetMasterWalletBalance(currency string) (float64, error)
		GetTransactionCommission(oType, wType string, wId *uint32, currency, to string, amount float64, memo string) (map[string]float64, error)
		GetTransactionStatus(hash, fromAddr string) (string, error)
		SendTransaction(wType string, wId *uint32, currency, to string, amount float64, memo string) (string, error)
		GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error)
	}
)

// ProcessInboundTransactions Process inbound transactions (scheduled every 10 sec)
// ctx: Process context from scheduler
// actionRepo: Action repository
func ProcessInboundTransactions(ctx context.Context, actionRepo *repository.ActionRepository) error {
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
				log.Printf("[ProcessInboundTransactions] [%s] Start processing [%d] user txs", network, len(txList))
			}

			// Process inbound transactions list
			for _, tx := range txList {
				log.Printf("[ProcessInboundTransactions] [%s] Start processing '%s' tx (%f %s)",
					network, tx.TxId, tx.Amount, tx.Currency)

				// Get tx currency rate from cache
				rate, _ := rates.GetClient().GetRate("crypto", tx.Currency, "USD")
				// Get tx amount in USD
				amountUsd := tx.Amount * rate

				var amlId string
				// Refill transaction's AML check, if tx amount in USD equals or higher AML threshold amount
				if amountUsd >= cfg.AmlThreshold {
					amlId, err = aml.GetClient().CheckTransaction(ctx, network, tx.Currency, tx.TxId, tx.To, "incoming")
					if err != nil {
						log.Printf("[ProcessInboundTransactions] [%s] AML check incoming tx '%s' failed: %v",
							network, tx.TxId, err)
						// Continue processing, even if AML check failed
					}
				}

				// Try to transfer tokens from user to master wallet and get transfer fee (in native network token)
				var transferFeeAmount float64
				minTransferAmount := config.TokenSettings[network][tx.Currency].MinTransferAmount
				if tx.Amount >= minTransferAmount {
					var (
						uHash          string
						transferStatus = "success"
					)
					transferFeeAmount, uHash, err = TransferTokensToMasterWallet(
						ctx, chainService, uint32(tx.UserId), tx.Currency, tx.To, tx.Amount,
					)
					if err != nil {
						log.Printf("[ProcessInboundTransactions] [%s] Failed to transfer tokens to master wallet: %v",
							network, err)
						transferStatus = "failed"

						// Continue processing, even if transfer tokens failed
					}

					// Register transfer action in DB
					_, err = actionRepo.Create(&models.Action{
						UserId:   tx.UserId,
						Type:     "transfer",
						Network:  network,
						Currency: tx.Currency,
						Amount:   tx.Amount,
						Hash:     uHash,
						Fee:      transferFeeAmount,
						Status:   transferStatus,
					})
					if err != nil {
						log.Printf("[ProcessInboundTransactions] [%s] Failed to create transfer action for user %d: %v",
							network, tx.UserId, err)
					}
				}

				// Encode refill tx item in JSON
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
					log.Printf("[ProcessInboundTransactions] [%s] Failed to marshal item for tx %s: %v",
						network, tx.TxId, err)
					continue
				}

				// Push item in Redis refills queue
				err = redis.GetClient().HSet(context.Background(), cfg.RedisRefillsKey, tx.TxId, string(txItem))
				if err != nil {
					log.Printf("[ProcessInboundTransactions] [%s] Failed to push tx '%s' to Redis: %v",
						network, tx.TxId, err)
				}

				// Register refill action in DB
				_, err = actionRepo.Create(&models.Action{
					UserId:   tx.UserId,
					Type:     "refill",
					Network:  network,
					Currency: tx.Currency,
					Amount:   tx.Amount,
					Hash:     tx.TxId,
					Fee:      transferFeeAmount,
					AmlId:    amlId,
					Status:   "success",
				})
				if err != nil {
					log.Printf("[ProcessInboundTransactions] [%s] Failed to create refill action for user %d: %v",
						network, tx.UserId, err)
				}

				log.Printf("[ProcessInboundTransactions] [%s] %f %s user transaction successfully processed",
					network, tx.Amount, tx.Currency)
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

// TransferTokensToMasterWallet Transfer tokens from user→Bybit wallet (with retrying)
// @note In current version at Bybit wallet
// ctx: Current process context
// chainService: Network service instance
// userId: User id
// currency: Token ticker
// userAddr: User wallet address
// amount: Amount in float value
// Returns: Transfer fee amount (in native network token value) and hash of user→Bybit tx
func TransferTokensToMasterWallet(
	ctx context.Context, chainService ChainService, userId uint32, currency, userAddr string, amount float64,
) (float64, string, error) {
	// Get network data from config
	network := chainService.GetCode()
	networkCfg, ok := config.GetNetworkConfig(network)
	if !ok {
		return 0, "", fmt.Errorf("[TransferTokensToMasterWallet] Unknown network: %s", network)
	}

	// Create retrier entity
	retrier := retry.New(
		retry.Attempts(3),
		retry.Delay(5*time.Second),
		retry.DelayType(retry.FixedDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Printf("[TransferTokensToMasterWallet] Retry attempt #%d: %v", n, err)
		}),
		retry.Context(ctx),
	)

	var (
		retryErr                                           error
		totalFeeAmount, transferFeeAmount, masterFeeAmount float64
	)

	// Calc needed fee amount (in native network token) for sending tokens from user to Bybit wallet
	retryErr = retrier.Do(
		func() error {
			// Calc fee amount for user→Bybit transfer
			userFee, err := chainService.GetTransactionCommission(
				"inner", "user", &userId, currency, networkCfg.BybitAddress, amount, "",
			)
			if err != nil {
				return fmt.Errorf("[TransferTokensToMasterWallet] Failed to get user→Bybit transaction commission: %w", err)
			}
			transferFeeAmount = userFee["chain_fee"]

			// Calc fee amount for master→user native token transfer
			if network != GetTon().GetCode() {
				masterFee, err := chainService.GetTransactionCommission(
					"inner", "master", &userId, networkCfg.NativeToken, userAddr, transferFeeAmount, "",
				)
				if err != nil {
					return fmt.Errorf("[TransferTokensToMasterWallet] Failed to get master→user transaction commission: %w", err)
				}
				masterFeeAmount = masterFee["chain_fee"]
			}

			// Calc total fee amount
			totalFeeAmount = transferFeeAmount + masterFeeAmount

			return nil
		},
	)
	if retryErr != nil {
		return 0, "", fmt.Errorf("[TransferTokensToMasterWallet] GetTransactionCommission step failed: %w", retryErr)
	}

	// Send native token for fee payment from master to user wallet, if currency != native token and if it's not TON network
	if network != GetTon().GetCode() && currency != networkCfg.NativeToken {
		retryErr = retrier.Do(
			func() error {
				// Check needed amount of native token in master wallet
				masterBalance, err := chainService.GetMasterWalletBalance(networkCfg.NativeToken)
				if err != nil {
					return fmt.Errorf("[TransferTokensToMasterWallet] Failed to get user %s balance: %w",
						networkCfg.NativeToken, err)
				}
				if masterBalance < masterFeeAmount {
					errMsg := fmt.Errorf("[TransferTokensToMasterWallet] Not enough master wallet %s balance (%f %s), needed %f %s",
						networkCfg.NativeToken, masterBalance, networkCfg.NativeToken, masterFeeAmount, networkCfg.NativeToken)

					// Send notifications to channels
					err := notifications.GetClient().SendNotifications(ctx, errMsg.Error())
					if err != nil {
						log.Printf("[TransferTokensToMasterWallet] Failed to send notifications: %v", err)
					}

					return errMsg
				}

				// Send transaction (master→user)
				fHash, err := chainService.SendTransaction("master", nil, networkCfg.NativeToken, userAddr, masterFeeAmount, "")
				if err != nil {
					return fmt.Errorf("[TransferTokensToMasterWallet] Failed to send native token (%f %s) from master to user: %w",
						masterFeeAmount, networkCfg.NativeToken, err)
				}

				log.Printf("[TransferTokensToMasterWallet] %f %s token for fee payment sent from master to user, hash: '%s'",
					masterFeeAmount, networkCfg.NativeToken, fHash)
				return nil
			},
		)
		if retryErr != nil {
			return totalFeeAmount, "", fmt.Errorf("[TransferTokensToMasterWallet] SendNativeTokenToUser step failed: %w", retryErr)
		}

		// Delay to wait for confirm native token transaction (block time seconds)
		select {
		case <-time.After(time.Duration(networkCfg.BlockTime) * time.Second):
		case <-ctx.Done():
			return totalFeeAmount, "", fmt.Errorf("[TransferTokensToMasterWallet] Context cancelled during delay: %w", ctx.Err())
		}
	}

	// Send tokens from user to Bybit wallet
	var hash string
	retryErr = retrier.Do(
		func() error {
			var err error

			// If currency = nativeToken, subtract commission from amount
			if currency == networkCfg.NativeToken && transferFeeAmount > 0 {
				amount = amount - transferFeeAmount
			}

			// If it's not TON network
			if network != GetTon().GetCode() {
				// Send transaction (user→Bybit) for all networks, excluding ton
				hash, err = chainService.SendTransaction("user", &userId, currency, networkCfg.BybitAddress, amount, "")
				if err != nil {
					return fmt.Errorf("[TransferTokensToMasterWallet] Failed to send tokens (%f %s) from user to Bybit: %w",
						amount, currency, err)
				}

				log.Printf("[TransferTokensToMasterWallet] [%s] %f %s transferred from user %d to Bybit, hash: '%s'",
					network, amount, currency, userId, hash)

			} else {
				// Send transaction (master→Bybit) for ton (no needed transfer for UMBRELLA jetton)
				if currency != "UMBRELLA" {
					hash, err = chainService.SendTransaction("master", nil, currency, networkCfg.BybitAddress, amount, "")
					if err != nil {
						return fmt.Errorf("[TransferTokensToMasterWallet] Failed to send tokens (%f %s) from master to Bybit: %w",
							amount, currency, err)
					}

					log.Printf("[TransferTokensToMasterWallet] [%s] %f %s transferred from master %d to Bybit, hash: '%s'",
						network, amount, currency, userId, hash)
				}
			}

			return nil
		},
	)
	if retryErr != nil {
		return totalFeeAmount, "", fmt.Errorf("[TransferTokensToMasterWallet] SendTokensToMaster step failed: %w", retryErr)
	}

	// Return master→user and user→bybit total transfers commission and hash of transfer to Bybit wallet
	return totalFeeAmount, hash, nil
}

// CreateWithdrawRequest Put new withdraw request in the queue and return queue_id
// userId: User id
// currency: Token ticker
// network: Network code
// to: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag (for TON only)
// amlId: AML address check id
// Returns: Withdraw request queue id
func CreateWithdrawRequest(userId uint, currency, network, to string, amount float64, memo, amlId string) (map[string]any, error) {
	ctx := context.Background()
	// Load config
	cfg, _ := config.Load()

	// Check Bybit wallet balance
	unifiedBalance, err := bybit.GetClient().GetWalletBalance(ctx, currency, "UNIFIED")
	if err != nil {
		return nil, err
	}
	fundBalance, err := bybit.GetClient().GetWalletBalance(ctx, currency, "FUND")
	if err != nil {
		return nil, err
	}
	totalBalance := unifiedBalance + fundBalance
	if totalBalance < amount {
		return nil, fmt.Errorf("[CreateWithdrawRequest] Insufficient funding amount to withdraw: Have %f on UNIFIED and %f on FUND, need %f %s",
			unifiedBalance, fundBalance, amount, currency)
	}

	// Get withdraw request item encoded in JSON
	reqItem, err := json.Marshal(map[string]any{
		"user_id":  userId,
		"currency": currency,
		"network":  network,
		"to":       to,
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
