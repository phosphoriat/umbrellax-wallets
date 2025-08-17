package bybit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"wallets/internal/config"

	bybit "github.com/bybit-exchange/bybit.go.api"
	"github.com/google/uuid"
)

// HTTPClient Bybit HTTP client struct
type HTTPClient struct {
	bybit *bybit.Client
}

// NewHTTPClient Create new Client instance
func NewHTTPClient(cfg *config.Config) *HTTPClient {
	return &HTTPClient{
		bybit: bybit.NewBybitHttpClient(cfg.BybitApiKey, cfg.BybitApiSecret, bybit.WithBaseURL(bybit.MAINNET)),
	}
}

// GetCoinInfo Get coin info for network
// @docs https://bybit-exchange.github.io/docs/v5/asset/coin-info
func (client *HTTPClient) GetCoinInfo(coin string, network string) (map[string]any, error) {
	params := map[string]interface{}{"coin": coin}
	response, err := client.bybit.NewUtaBybitServiceWithParams(params).GetCoinInfo(context.Background())
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return nil, fmt.Errorf("API error: %s", response.RetMsg)
	}

	result := make(map[string]any)
	if resultMap, ok := response.Result.(map[string]interface{}); ok {
		if rows, ok := resultMap["rows"].([]interface{}); ok {
			if len(rows) > 0 {
				row := rows[0].(map[string]interface{})

				result["coin"] = row["coin"]
				result["name"] = row["name"]

				// Try to find network
				networkFound := false
				if chains, ok := row["chains"].([]interface{}); ok {
					for _, chainInterface := range chains {
						chainMap := chainInterface.(map[string]interface{})
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

// GetWithdrawalFee Get withdrawal fee for coin in network
func (client *HTTPClient) GetWithdrawalFee(coin string, network string) (float64, error) {
	result, err := client.GetCoinInfo(coin, network)
	if err != nil {
		return 0, err
	}

	if networkData, ok := result["network"].(map[string]interface{}); ok {
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

// GetMasterWalletAddress Get master wallet address
// @docs https://bybit-exchange.github.io/docs/v5/asset/deposit/master-deposit-addr
func (client *HTTPClient) GetMasterWalletAddress(coin string, network string) (string, error) {
	params := map[string]interface{}{
		"coin":      coin,
		"chainType": strings.ToUpper(network),
	}
	response, err := client.bybit.NewUtaBybitServiceWithParams(params).GetMasterAccDepositAddress(context.Background())
	if err != nil {
		return "", fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return "", fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]interface{}); ok {
		if chains, ok := resultMap["chains"].([]interface{}); ok {
			for _, chainInterface := range chains {
				if chainMap, ok := chainInterface.(map[string]interface{}); ok {
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
func (client *HTTPClient) GetWalletBalance(coin string) (float64, error) {
	params := map[string]interface{}{
		"accountType": "UNIFIED",
		"coin":        coin,
	}
	response, err := client.bybit.NewUtaBybitServiceWithParams(params).GetAccountWallet(context.Background())
	if err != nil {
		return 0, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return 0, fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]interface{}); ok {
		if list, ok := resultMap["list"].([]interface{}); ok {
			if len(list) > 0 {
				account := list[0].(map[string]interface{})
				if coins, ok := account["coin"].([]interface{}); ok {
					for _, coinInterface := range coins {
						coinMap := coinInterface.(map[string]interface{})
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
func (client *HTTPClient) GetWithdrawalRecords(coin string, startTime *string) ([]interface{}, error) {
	params := map[string]interface{}{
		"coin": coin,
	}
	if startTime != nil {
		params["startTime"] = *startTime
	}

	response, err := client.bybit.NewUtaBybitServiceWithParams(params).GetWithdrawalRecords(context.Background())
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return nil, fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]interface{}); ok {
		if rows, ok := resultMap["rows"].([]interface{}); ok {
			return rows, nil
		}
	}

	return nil, fmt.Errorf("invalid response format")
}

// CreateWithdraw Create new withdraw
// @docs https://bybit-exchange.github.io/docs/v5/asset/withdraw
func (client *HTTPClient) CreateWithdraw(coin string, chain string, address string, amount float64, tag *string) (int, error) {
	params := map[string]interface{}{
		"coin":        coin,
		"chain":       chain,
		"address":     address,
		"amount":      strconv.FormatFloat(amount, 'f', 8, 64),
		"timestamp":   strconv.FormatInt(time.Now().UnixMilli(), 10),
		"accountType": "UNIFIED",
	}
	if tag != nil && *tag != "" {
		params["tag"] = *tag
	}

	response, err := client.bybit.NewUtaBybitServiceWithParams(params).CreateWithdraw(context.Background())
	if err != nil {
		return 0, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return 0, fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]interface{}); ok {
		if idStr, ok := resultMap["id"].(string); ok {
			id, err := strconv.Atoi(idStr)
			if err != nil {
				return 0, fmt.Errorf("failed to parse withdraw ID '%s': %w", idStr, err)
			}

			// @TODO Add withdraw id to Redis bybit_withdrawals queue

			return id, nil
		}
	}

	return 0, fmt.Errorf("invalid response format")
}

// CreateInternalTransfer Create internal transfer
// @docs https://bybit-exchange.github.io/docs/v5/asset/transfer/create-inter-transfer
func (client *HTTPClient) CreateInternalTransfer(coin string, amount string, fromAccountType string, toAccountType string) (int, error) {
	params := map[string]interface{}{
		"transferId":      uuid.NewString(),
		"coin":            coin,
		"amount":          amount,
		"fromAccountType": fromAccountType,
		"toAccountType":   toAccountType,
	}
	response, err := client.bybit.NewUtaBybitServiceWithParams(params).CreateInternalTransfer(context.Background())
	if err != nil {
		return 0, fmt.Errorf("request error: %w", err)
	}
	if response.RetCode != 0 {
		return 0, fmt.Errorf("API error: %s", response.RetMsg)
	}

	if resultMap, ok := response.Result.(map[string]interface{}); ok {
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
