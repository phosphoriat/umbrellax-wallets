package bybit

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/vault"
	"wallets/internal/utils"

	bybitApi "github.com/bybit-exchange/bybit.go.api"
	"github.com/google/uuid"
)

// HTTPClient Bybit HTTP client struct
type HTTPClient struct {
	bybit *bybitApi.Client
	ctx   context.Context
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
	})
}

// @private newHTTPClient Create new Client instance (private constructor)
func newHTTPClient(cfg *config.Config) *HTTPClient {
	return &HTTPClient{
		bybit: bybitApi.NewBybitHttpClient(cfg.BybitApiKey, cfg.BybitApiSecret, bybitApi.WithBaseURL(bybitApi.MAINNET)),
		ctx:   context.Background(),
	}
}

// GetCoinInfo
// Get coin info for network
// @docs https://bybit-exchange.github.io/docs/v5/asset/coin-info
func (c *HTTPClient) GetCoinInfo(coin, network string) (map[string]any, error) {
	resultMap, err := c.sendRequest(map[string]any{"coin": coin}, "GetCoinInfo")
	if err != nil {
		return nil, err
	}

	rowData, ok := utils.DataGet(resultMap, "rows.0")
	if !ok {
		return nil, fmt.Errorf("coin '%s' not found", coin)
	}
	result := map[string]any{
		"coin": utils.DataGet(rowData, "coin"),
		"name": utils.DataGet(rowData, "name"),
	}

	chains, ok := utils.DataGet(rowData, "chains")
	if !ok {
		return nil, fmt.Errorf("network '%s' not found for coin '%s'", network, coin)
	}

	for _, chainItem := range chains.([]any) {
		if chainName, _ := utils.DataGet(chainItem, "chain"); chainName == network {
			result["network"] = chainItem
			return result, nil
		}
	}

	return nil, fmt.Errorf("network '%s' not found for coin '%s'", network, coin)
}

// GetWithdrawalFee
// Get withdrawal fee for coin in network (in coin value)
func (c *HTTPClient) GetWithdrawalFee(coin, network string) (float64, error) {
	result, err := c.GetCoinInfo(coin, network)
	if err != nil {
		return 0, err
	}

	if feeData, ok := utils.DataGet(result, "network.withdrawFee"); ok {
		if feeStr, ok := feeData.(string); ok {
			if feeVal, err := strconv.ParseFloat(feeStr, 64); err == nil {
				return feeVal, nil
			}
		}
	}

	return 0, fmt.Errorf("withdrawal fee not found for coin '%s' on network '%s'", coin, network)
}

// @private getMinWithdrawalAmount
// Get min withdrawal amount for coin in network
func (c *HTTPClient) getMinWithdrawalAmount(coin, network string) (float64, error) {
	result, err := c.GetCoinInfo(coin, network)
	if err != nil {
		return 0, err
	}

	if minData, ok := utils.DataGet(result, "network.withdrawMin"); ok {
		if minStr, ok := minData.(string); ok {
			if minVal, err := strconv.ParseFloat(minStr, 64); err == nil {
				return minVal, nil
			}
		}
	}

	return 0, fmt.Errorf("min withdrawal amount not found for coin '%s' on network '%s'", coin, network)
}

// GetMasterWalletAddress
// Get master wallet address
// @docs https://bybit-exchange.github.io/docs/v5/asset/deposit/master-deposit-addr
func (c *HTTPClient) GetMasterWalletAddress(coin, network string) (string, error) {
	resultMap, err := c.sendRequest(map[string]any{
		"coin":      coin,
		"chainType": strings.ToUpper(network),
	}, "GetMasterAccDepositAddress")
	if err != nil {
		return "", err
	}

	if chains, ok := utils.DataGet(resultMap, "chains"); ok {
		for _, chainItem := range chains.([]any) {
			if chainType, _ := utils.DataGet(chainItem, "chainType"); chainType != nil {
				if chainTypeStr, ok := chainType.(string); ok && strings.EqualFold(chainTypeStr, network) {
					if address, ok := utils.DataGet(chainItem, "addressDeposit"); ok {
						if addressStr, ok := address.(string); ok {
							return addressStr, nil
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("deposit address for coin '%s' on network '%s' not found", coin, network)
}

// GetWalletBalance
// Get wallet balance
// @docs https://bybit-exchange.github.io/docs/v5/account/wallet-balance
func (c *HTTPClient) GetWalletBalance(coin, accountType string) (float64, error) {
	resultMap, err := c.sendRequest(map[string]any{
		"coin":        coin,
		"accountType": accountType,
	}, "GetAccountWallet")
	if err != nil {
		return 0, err
	}

	if coinsData, ok := utils.DataGet(resultMap, "list.0.coin"); ok {
		if coins, ok := coinsData.([]any); ok {
			for _, coinItem := range coins {
				if coinName, _ := utils.DataGet(coinItem, "coin"); coinName == coin {
					if balanceStr, ok := utils.DataGet(coinItem, "walletBalance"); ok {
						if balance, err := strconv.ParseFloat(balanceStr.(string), 64); err == nil {
							return balance, nil
						}
					}
				}
			}
		}
	}

	return 0, fmt.Errorf("coin '%s' not found", coin)
}

// GetWithdrawalRecords
// Get withdrawal records
// @docs https://bybit-exchange.github.io/docs/v5/asset/withdraw/withdraw-record
func (c *HTTPClient) GetWithdrawalRecords(params map[string]any) ([]any, error) {
	resultMap, err := c.sendRequest(params, "GetWithdrawalRecords")
	if err != nil {
		return nil, err
	}

	if rows, ok := utils.DataGet(resultMap, "rows"); ok {
		return rows.([]any), nil
	}
	return nil, fmt.Errorf("invalid response format")
}

// CreateWithdraw
// Create new withdraw from bybit to master wallet and return bybit withdraw id
// Withdrawals can be only from FUND account type
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

	// Transfer needed amount from UNIFIED to FUND balance to withdraw
	_, err = c.CreateInternalTransfer(coin, strconv.FormatFloat(amount, 'g', -1, 64), "UNIFIED", "FUND")
	if err != nil {
		return "", fmt.Errorf("failed to create internal transfer: %w", err)
	}

	// Send withdraw request from FUND account to master wallet
	resultMap, err := c.sendRequest(map[string]any{
		"coin":        coin,
		"chain":       chain,
		"address":     masterAddress,
		"amount":      strconv.FormatFloat(amount, 'f', 8, 64),
		"timestamp":   time.Now().UnixMilli(),
		"accountType": "FUND",
		"forceChain":  1,
	}, "CreateWithdraw")
	if err != nil {
		return "", err
	}

	if result, ok := utils.DataGet(resultMap, "id"); ok {
		return result.(string), nil
	}
	return "", fmt.Errorf("invalid response format")
}

// CreateInternalTransfer
// Create internal account transfer (f.e. from UNIFIED to FUND)
// @docs https://bybit-exchange.github.io/docs/v5/asset/transfer/create-inter-transfer
func (c *HTTPClient) CreateInternalTransfer(coin, amount, fromAccountType, toAccountType string) (int, error) {
	resultMap, err := c.sendRequest(map[string]any{
		"transferId":      uuid.NewString(),
		"coin":            coin,
		"amount":          amount,
		"fromAccountType": fromAccountType,
		"toAccountType":   toAccountType,
	}, "CreateInternalTransfer")
	if err != nil {
		return 0, err
	}

	if idStr, ok := utils.DataGet(resultMap, "id"); ok {
		id, err := strconv.Atoi(idStr.(string))
		if err != nil {
			return 0, fmt.Errorf("failed to parse transfer ID '%s': %w", idStr, err)
		}
		return id, nil
	}
	return 0, fmt.Errorf("invalid response format")
}

// @private sendRequest
// Send request to Bybit API and return response result or error
func (c *HTTPClient) sendRequest(params map[string]any, methodName string) (map[string]any, error) {
	reqClient := c.bybit.NewUtaBybitServiceWithParams(params)

	v := reflect.ValueOf(reqClient)
	method := v.MethodByName(methodName)

	if !method.IsValid() || method.Kind() != reflect.Func {
		return nil, fmt.Errorf("invalid request method: %s", methodName)
	}

	args := []reflect.Value{reflect.ValueOf(c.ctx)}
	callRes := method.Call(args)
	// Check call result length
	if len(callRes) != 2 {
		return nil, fmt.Errorf("method %s returned %d values, expected 2", methodName, len(callRes))
	}

	// Get API response value
	responseVal := callRes[0]
	if !responseVal.IsValid() || responseVal.IsNil() {
		return nil, fmt.Errorf("method %s returned invalid or nil response", methodName)
	}
	serverResponse, ok := responseVal.Interface().(*bybitApi.ServerResponse)
	if !ok {
		return nil, fmt.Errorf("method %s returned unexpected response type: %T", methodName, responseVal.Interface())
	}

	// Get API error value
	errorVal := callRes[1]
	if errorVal.IsValid() && !errorVal.IsNil() {
		if serverError, ok := errorVal.Interface().(error); ok && serverError != nil {
			return nil, fmt.Errorf("HTTP request error for method %s: %w", methodName, serverError)
		} else if !ok {
			return nil, fmt.Errorf("method %s returned invalid error type: %T", methodName, errorVal.Interface())
		}
	}

	// Check API response
	if serverResponse.RetCode != 0 {
		return nil, fmt.Errorf("API error for method %s (code: %d): %s", methodName, serverResponse.RetCode, serverResponse.RetMsg)
	}

	// Extract result
	if serverResponse.Result == nil {
		return make(map[string]any), nil
	}
	result, ok := serverResponse.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("method %s returned unexpected result type: %T", methodName, serverResponse.Result)
	}

	return result, nil
}
