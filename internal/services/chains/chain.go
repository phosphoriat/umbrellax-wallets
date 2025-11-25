package chains

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/database/vault"
	"wallets/internal/repository"
	"wallets/internal/services/api/aml"

	"github.com/avast/retry-go/v5"
	"github.com/google/uuid"
)

type (
	// BaseChainService Common fields struct for all chains
	BaseChainService struct {
		WalletRepo  *repository.WalletRepository
		NetworkCode string
		NativeToken string
		AmlTokenId  string
	}

	// ChainService Interface with all chain methods
	ChainService interface {
		GetCode() string
		GetUserWallet(userId int) (string, error)
		GetMasterWallet() (map[string]any, error)
		GetTransactionCommission(wType string, wId *string, currency, to string, amount float64, memo string) (map[string]float64, error)
		GetTransactionStatus(hash, toAddr string) (string, error)
		SendTransaction(wType string, wId *string, currency, to string, amount float64, memo string) (string, string, error)
		GetInboundTransactions() ([]map[string]any, error)
	}
)

// ProcessInboundTransactions Process inbound transactions
func ProcessInboundTransactions() error {
	// Load config
	cfg, _ := config.Load()

	// Get all networks list
	for _, network := range GetAvailableNetworks() {
		// Get chain service instance for network
		chainService, err := GetChainService(network)
		if err != nil {
			log.Printf("failed to get %s chain service: %v", network, err)
			continue
		}

		// Process transactions in separate goroutine for every network
		go func() {
			txList, err := chainService.GetInboundTransactions()
			if err != nil {
				log.Printf("failed to get %s inbound transactions: %v", network, err)
				return
			}

			for _, tx := range txList {
				userId, okU := tx["userId"].(int)
				currency, okC := tx["currency"].(string)
				hash, okH := tx["txId"].(string)
				from, _ := tx["from"].(string)
				to, okT := tx["to"].(string)
				amount, okA := tx["amount"].(float64)
				if !okU || !okC || !okH || !okT || !okA {
					log.Printf("Invalid inbound transaction data: %+v", tx)
					continue
				}

				// Refill transaction AML check
				amlId, err := aml.GetClient().CheckTransaction(network, currency, hash, to, "incoming")
				if err != nil {
					log.Printf("AML check inbound transaction '%s' at network '%s' failed: %v", hash, network, err)
				}

				// Transfer tokens from user wallet to master wallet and return transfer fee (in native network token)
				transferFeeAmount, err := transferTokensToMasterWallet(chainService, strconv.Itoa(userId), currency, to, amount)

				// Get refill tx item encoded in JSON
				txItem, _ := json.Marshal(map[string]any{
					"user_id":  userId,
					"currency": currency,
					"network":  network,
					"hash":     hash,
					"from":     from,
					"to":       to,
					"amount":   amount,
					"fee":      transferFeeAmount,
					"aml_id":   amlId,
				})

				// Push item in Redis refills queue
				redis.GetClient().HSet(context.Background(), cfg.RedisRefillsKey, network, string(txItem))
			}
		}()
	}

	return nil
}

// @private transferTokensToMasterWallet
// Grab tokens from user wallet to master wallet (with retrying). In current version at Bybit wallet
// chainService: Network service instance
// userId: User id
// currency: Token ticker
// userAddr: User wallet address
// amount: Amount in float value
// Returns: Transfer fee amount (in native network token value)
func transferTokensToMasterWallet(chainService ChainService, userId, currency, userAddr string, amount float64) (float64, error) {
	network := chainService.GetCode()

	var masterAddr, nativeToken string
	var transferFeeAmount float64

	// Create retrier with options
	retrier := retry.New(
		retry.Attempts(3),
		retry.Delay(2*time.Second),
		retry.DelayType(retry.FixedDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Printf("TransferTokensToMasterWallet retry attempt %d: %v", n, err)
		}),
	)

	// Get master wallet data from Vault and native token from config
	err := retrier.Do(
		func() error {
			masterData, err := vault.GetClient().GetSecret("/master/" + network)
			if err != nil {
				return fmt.Errorf("failed to get master wallet data: %w", err)
			}
			addr, ok := masterData["address"].(string)
			if !ok || addr == "" {
				return fmt.Errorf("invalid or missing %s wallet address in Vault", network)
			}
			masterAddr = addr

			token, err := config.GetChainSetting[string](config.NetworkSettings, fmt.Sprintf("%s.native_token", network))
			if err != nil {
				return fmt.Errorf("failed to get native_token for %s: %w", network, err)
			}
			nativeToken = token

			return nil
		},
	)
	if err != nil {
		return 0, fmt.Errorf("GetMasterWalletData failed: %w", err)
	}

	// Calc needed fee amount (in native network token) for sending tokens from user to master wallet
	err = retrier.Do(
		func() error {
			fee, err := chainService.GetTransactionCommission("user", &userId, currency, masterAddr, amount, "")
			if err != nil {
				return fmt.Errorf("failed to get transaction commission: %w", err)
			}
			transferFeeAmount = fee["chain_fee"]

			return nil
		},
	)
	if err != nil {
		return 0, fmt.Errorf("GetTransactionCommission failed: %w", err)
	}

	// Send native token for fee payment from master to user wallet
	// Send native token amount (for fee payment) from master to user wallet
	err = retrier.Do(
		func() error {
			hash, _, err := chainService.SendTransaction("master", nil, nativeToken, userAddr, transferFeeAmount, "")
			if err != nil {
				return fmt.Errorf("failed to send native token from master to user: %w", err)
			}

			log.Printf("%s token sent from master to user, hash: %s", nativeToken, hash)
			return nil
		},
	)
	if err != nil {
		return 0, fmt.Errorf("SendNativeTokenToUser failed: %w", err)
	}

	// Slight delay to confirm native token transaction
	time.Sleep(2 * time.Second)

	// Send tokens from user to master wallet
	err = retrier.Do(
		func() error {
			uHash, _, err := chainService.SendTransaction("user", &userId, currency, masterAddr, amount, "")
			if err != nil {
				return fmt.Errorf("failed to send tokens from user to master: %w", err)
			}

			log.Printf("%s sent from user %s to master, hash: %s", currency, userId, uHash)
			return nil
		},
	)
	if err != nil {
		return 0, fmt.Errorf("SendTokensToMaster failed: %w", err)
	}

	return transferFeeAmount, nil
}

// CreateWithdrawRequest Put new withdraw request in the queue and return queue_id
// currency: Token ticker
// network: Network code
// address: Withdrawal address
// amount: Amount in float value
// memo: Memo tag (for TON only)
func CreateWithdrawRequest(currency, network, address string, amount float64, memo string) (map[string]any, error) {
	// Load config
	cfg, _ := config.Load()

	// Get withdraw request item encoded in JSON
	reqItem, err := json.Marshal(map[string]any{
		"currency": currency,
		"network":  network,
		"address":  address,
		"amount":   amount,
		"memo":     memo,
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
