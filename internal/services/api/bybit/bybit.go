package bybit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/vault"

	bybitApi "github.com/bybit-exchange/bybit.go.api"
	"github.com/google/uuid"
)

// HTTPClient Bybit HTTP client struct
type HTTPClient struct {
	bybit *bybitApi.Client
}

// Global package vars
var (
	clientInstance *HTTPClient
	once           sync.Once
)

// GetHTTPClient Get global Bybit HTTPClient instance
func GetHTTPClient() *HTTPClient {
	return clientInstance
}

// Init Bybit service
func Init(cfg *config.Config) {
	once.Do(func() {
		clientInstance = newHTTPClient(cfg)

		// Init Bybit websocket wallet stream
		InitWalletStream(cfg)
		// Init Bybit withdrawals read from Redis hash
		InitWithdrawalsRead(cfg)
	})
}

// @private newHTTPClient Create new Client instance (private constructor)
func newHTTPClient(cfg *config.Config) *HTTPClient {
	return &HTTPClient{
		bybit: bybitApi.NewBybitHttpClient(cfg.BybitApiKey, cfg.BybitApiSecret, bybitApi.WithBaseURL(bybitApi.MAINNET)),
	}
}

// GetCoinInfo Get coin info for network
// @docs https://bybit-exchange.github.io/docs/v5/asset/coin-info
func (c *HTTPClient) GetCoinInfo(coin, network string) (map[string]any, error) {
	params := map[string]any{"coin": coin}
	response, err := c.bybit.NewUtaBybitServiceWithParams(params).GetCoinInfo(context.Background())
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return nil, fmt.Errorf("API error: %s", response.RetMsg)
	}

	result := make(map[string]any)
	if resultMap, ok := response.Result.(map[string]any); ok {
		if rows, ok := resultMap["rows"].([]any); ok {
			if len(rows) > 0 {
				row := rows[0].(map[string]any)

				result["coin"] = row["coin"]
				result["name"] = row["name"]

				// Try to find network
				networkFound := false
				if chains, ok := row["chains"].([]any); ok {
					for _, chainInterface := range chains {
						chainMap := chainInterface.(map[string]any)
						if chainMap["chain"].(string) == network {
							result["network"] = chainMap
							networkFound = true
							break
						}
					}
				}

				if !networkFound {
					return nil, fmt.Errorf("network '%s' not found for coin '%s'", network, coin)
				}
				return result, nil

			} else {
				return nil, fmt.Errorf("coin '%s' not found", coin)
			}
		}
	}

	return nil, fmt.Errorf("invalid response format")
}

// @private getWithdrawalFee Get withdrawal fee for coin in network
func (c *HTTPClient) getWithdrawalFee(coin, network string) (float64, error) {
	result, err := c.GetCoinInfo(coin, network)
	if err != nil {
		return 0, err
	}

	if networkData, ok := result["network"].(map[string]any); ok {
		if feeStr, ok := networkData["withdrawFee"].(string); ok {
			fee, err := strconv.ParseFloat(feeStr, 64)
			if err != nil {
				return 0, fmt.Errorf("failed to parse withdrawal fee: %w", err)
			}
			return fee, nil
		}
	}

	return 0, fmt.Errorf("withdrawal fee not found for coin '%s' on network '%s'", coin, network)
}

// @private getMinWithdrawalAmount Get min withdrawal amount for coin in network
func (c *HTTPClient) getMinWithdrawalAmount(coin, network string) (float64, error) {
	result, err := c.GetCoinInfo(coin, network)
	if err != nil {
		return 0, err
	}

	if networkData, ok := result["network"].(map[string]any); ok {
		if feeStr, ok := networkData["withdrawMin"].(string); ok {
			fee, err := strconv.ParseFloat(feeStr, 64)
			if err != nil {
				return 0, fmt.Errorf("failed to parse withdrawal fee: %w", err)
			}
			return fee, nil
		}
	}

	return 0, fmt.Errorf("min withdrawal amount not found for coin '%s' on network '%s'", coin, network)
}

// GetMasterWalletAddress Get master wallet address
// @docs https://bybit-exchange.github.io/docs/v5/asset/deposit/master-deposit-addr
func (c *HTTPClient) GetMasterWalletAddress(coin, network string) (string, error) {
	params := map[string]any{
		"coin":      coin,
		"chainType": strings.ToUpper(network),
	}
	response, err := c.bybit.NewUtaBybitServiceWithParams(params).GetMasterAccDepositAddress(context.Background())
	if err != nil {
		return "", fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return "", fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]any); ok {
		if chains, ok := resultMap["chains"].([]any); ok {
			for _, chainInterface := range chains {
				if chainMap, ok := chainInterface.(map[string]any); ok {
					if chainType, ok := chainMap["chainType"].(string); ok {
						if strings.EqualFold(chainType, network) {
							if address, ok := chainMap["addressDeposit"].(string); ok {
								return address, nil
							}
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("deposit address not found for coin '%s' on network '%s'", coin, network)
}

// GetWalletBalance Get wallet balance
// @docs https://bybit-exchange.github.io/docs/v5/account/wallet-balance
func (c *HTTPClient) GetWalletBalance(coin, accountType string) (float64, error) {
	params := map[string]any{
		"coin":        coin,
		"accountType": accountType,
	}
	response, err := c.bybit.NewUtaBybitServiceWithParams(params).GetAccountWallet(context.Background())
	if err != nil {
		return 0, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return 0, fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]any); ok {
		if list, ok := resultMap["list"].([]any); ok {
			if len(list) > 0 {
				account := list[0].(map[string]any)
				if coins, ok := account["coin"].([]any); ok {
					for _, coinInterface := range coins {
						coinMap := coinInterface.(map[string]any)
						if coinMap["coin"].(string) == coin {
							if balanceStr, ok := coinMap["walletBalance"].(string); ok {
								balance, err := strconv.ParseFloat(balanceStr, 64)
								if err != nil {
									return 0, fmt.Errorf("failed to parse balance: %w", err)
								}
								return balance, nil
							}
						}
					}
				}
			}
		}
	}

	return 0, fmt.Errorf("coin '%s' not found", coin)
}

// GetWithdrawalRecords Get withdrawal records
// @docs https://bybit-exchange.github.io/docs/v5/asset/withdraw/withdraw-record
func (c *HTTPClient) GetWithdrawalRecords(coin string, startTime *string) ([]any, error) {
	params := map[string]any{
		"coin": coin,
	}
	if startTime != nil {
		params["startTime"] = *startTime
	}

	response, err := c.bybit.NewUtaBybitServiceWithParams(params).GetWithdrawalRecords(context.Background())
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return nil, fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]any); ok {
		if rows, ok := resultMap["rows"].([]any); ok {
			return rows, nil
		}
	}

	return nil, fmt.Errorf("invalid response format")
}

// CreateWithdraw Create new withdraw from bybit to master wallet and return withdraw id
// @docs https://bybit-exchange.github.io/docs/v5/asset/withdraw
func (c *HTTPClient) CreateWithdraw(coin, chain string, amount float64) (string, error) {
	// Get master wallet address for network from Vault
	masterAddress, err := vault.GetClient().GetSecret("/master/" + chain)
	if err != nil {
		return "", fmt.Errorf("failed to get master address: %w", err)
	}

	// Get min withdrawal amount for network from coin info method
	minWithdrawal, err := c.getMinWithdrawalAmount(coin, chain)
	if err != nil {
		return "", fmt.Errorf("failed to get min withdrawal amount: %w", err)
	}

	// Check amount for min withdrawal value for coin in network
	if amount < minWithdrawal {
		return "", fmt.Errorf("amount is less than the min withdrawal amount")
	}

	// Get FUND wallet balance for coin
	walletBalance, err := c.GetWalletBalance(coin, "FUND")
	if err != nil {
		return "", fmt.Errorf("failed to get wallet balance: %w", err)
	}

	// Check FUND wallet balance and return error, if not enough amount
	if walletBalance < amount {
		return "", fmt.Errorf("not enough FUND balance amount to withdraw. Needed %f, but %f available", amount, walletBalance)
	}

	// Params for withdraw request to master wallet (step 1)
	params := map[string]any{
		"coin":        coin,
		"chain":       chain,
		"address":     masterAddress,
		"amount":      strconv.FormatFloat(amount, 'f', 8, 64),
		"timestamp":   time.Now().UnixMilli(),
		"accountType": "FUND",
		"forceChain":  1,
	}

	response, err := c.bybit.NewUtaBybitServiceWithParams(params).CreateWithdraw(context.Background())
	if err != nil || response.RetCode != 0 {
		if err != nil {
			return "", fmt.Errorf("request error: %w", err)
		}
		if response.RetCode != 0 {
			return "", fmt.Errorf("API error: %s", response.RetMsg)
		}
	}

	if resultMap, ok := response.Result.(map[string]any); ok {
		return resultMap["id"].(string), nil
	}

	return "", fmt.Errorf("invalid response format")
}

// CreateInternalTransfer Create internal account transfer (fe from FUND to UNIFIED)
// @docs https://bybit-exchange.github.io/docs/v5/asset/transfer/create-inter-transfer
func (c *HTTPClient) CreateInternalTransfer(coin, amount, fromAccountType, toAccountType string) (int, error) {
	params := map[string]any{
		"transferId":      uuid.NewString(),
		"coin":            coin,
		"amount":          amount,
		"fromAccountType": fromAccountType,
		"toAccountType":   toAccountType,
	}
	response, err := c.bybit.NewUtaBybitServiceWithParams(params).CreateInternalTransfer(context.Background())
	if err != nil {
		return 0, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return 0, fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]any); ok {
		if idStr, ok := resultMap["id"].(string); ok {
			id, err := strconv.Atoi(idStr)
			if err != nil {
				return 0, fmt.Errorf("failed to parse transfer ID '%s': %w", idStr, err)
			}

			return id, nil
		}
	}

	return 0, fmt.Errorf("invalid response format")
}
