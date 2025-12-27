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
}

// Global package vars
var (
	clientInstance *HTTPClient
	once           sync.Once
)

// GetClient Get global Bybit HTTPClient instance
func GetClient() *HTTPClient {
	once.Do(func() {
		clientInstance = newHTTPClient()
	})
	return clientInstance
}

// @private newHTTPClient Create new Client instance (private constructor)
func newHTTPClient() *HTTPClient {
	// Load config
	cfg, _ := config.Load()

	return &HTTPClient{
		bybit: bybitApi.NewBybitHttpClient(
			cfg.BybitApiKey, cfg.BybitApiSecret, bybitApi.WithBaseURL(bybitApi.MAINNET),
		),
	}
}

// GetCoinInfo Get coin info for network
// coin: Currency ticker
// network: Network code
// @docs https://bybit-exchange.github.io/docs/v5/asset/coin-info
func (c *HTTPClient) GetCoinInfo(ctx context.Context, coin, network string) (map[string]any, error) {
	resultMap, err := c.sendRequest(ctx, map[string]any{"coin": coin}, "GetCoinInfo")
	if err != nil {
		return nil, err
	}

	rowData, ok := utils.DataGet(resultMap, "rows.0")
	if !ok {
		return nil, fmt.Errorf("[Bybit API] GetCoinInfo: Coin '%s' not found", coin)
	}

	coinValue, _ := utils.DataGet(rowData, "coin")
	nameValue, _ := utils.DataGet(rowData, "name")
	result := map[string]any{
		"coin": coinValue,
		"name": nameValue,
	}

	chains, ok := utils.DataGet(rowData, "chains")
	if !ok {
		return nil, fmt.Errorf("[Bybit API] GetCoinInfo: Network '%s' not found for coin '%s'", network, coin)
	}
	chainsList, ok := chains.([]any)
	if !ok {
		return nil, fmt.Errorf("[Bybit API] GetCoinInfo: Invalid chains format")
	}

	for _, chainItem := range chainsList {
		if chainName, _ := utils.DataGet(chainItem, "chain"); chainName == network {
			result["network"] = chainItem
			return result, nil
		}
	}
	return nil, fmt.Errorf("[Bybit API] GetCoinInfo: Network '%s' not found for coin '%s'", network, coin)
}

// GetDepositAddress Get deposit wallet address
// @note Unused
// coin: Currency ticker
// network: Network code
// @docs https://bybit-exchange.github.io/docs/v5/asset/deposit/master-deposit-addr
func (c *HTTPClient) GetDepositAddress(ctx context.Context, coin, network string) (string, error) {
	resultMap, err := c.sendRequest(ctx, map[string]any{
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
	return "", fmt.Errorf("[Bybit API] GetDepositAddress: Deposit address for coin '%s' on network '%s' not found",
		coin, network)
}

// GetWalletBalance Get wallet balance
// @note Unused
// coin: Currency ticker
// accountType: Account type (FUND, UNIFIED)
// @docs https://bybit-exchange.github.io/docs/v5/account/wallet-balance
func (c *HTTPClient) GetWalletBalance(ctx context.Context, coin, accountType string) (float64, error) {
	resultMap, err := c.sendRequest(ctx, map[string]any{
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
	return 0, fmt.Errorf("[Bybit API] GetWalletBalance: Coin '%s' not found", coin)
}

// GetWithdrawalFee Get withdrawal fee for coin in network (in coin value)
// coin: Currency ticker
// network: Network code
func (c *HTTPClient) GetWithdrawalFee(ctx context.Context, coin, network string) (float64, error) {
	result, err := c.GetCoinInfo(ctx, coin, network)
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
	return 0, fmt.Errorf("[Bybit API] GetWithdrawalFee: Withdrawal fee not found for coin '%s' on network '%s'",
		coin, network)
}

// GetWithdrawalRecords Get withdrawal records
// params: Params data
// @docs https://bybit-exchange.github.io/docs/v5/asset/withdraw/withdraw-record
func (c *HTTPClient) GetWithdrawalRecords(ctx context.Context, params map[string]any) ([]any, error) {
	resultMap, err := c.sendRequest(ctx, params, "GetWithdrawalRecords")
	if err != nil {
		return nil, err
	}

	if rows, ok := utils.DataGet(resultMap, "rows"); ok {
		if rows, ok := rows.([]any); ok {
			return rows, nil
		}
	}
	return nil, fmt.Errorf("[Bybit API] GetWithdrawalRecords: Invalid response format")
}

// CreateWithdraw Create new withdraw from bybit to master wallet and return bybit withdraw id
// Withdrawals can be only from FUND account type
// coin: Currency ticker
// chain: Network code
// amount: Withdraw amount value
// @docs https://bybit-exchange.github.io/docs/v5/asset/withdraw
func (c *HTTPClient) CreateWithdraw(ctx context.Context, coin, chain string, amount float64) (string, error) {
	// Get master wallet address for network from Vault
	masterAddress, err := vault.GetClient().GetSecret("/master/" + chain)
	if err != nil {
		return "", fmt.Errorf("[Bybit API] CreateWithdraw: Failed to get master address: %w", err)
	}

	// Get min withdrawal amount for network from coin info method
	minWithdrawal, err := c.getMinWithdrawalAmount(ctx, coin, chain)
	if err != nil {
		return "", fmt.Errorf("[Bybit API] CreateWithdraw: Failed to get min withdrawal amount: %w", err)
	}

	// Check amount for min withdrawal value for coin in network
	if amount < minWithdrawal {
		return "", fmt.Errorf("[Bybit API] CreateWithdraw: Amount is less than the min withdrawal amount")
	}

	// Transfer needed amount from UNIFIED to FUND balance to withdraw
	_, err = c.createInternalTransfer(ctx, coin, strconv.FormatFloat(amount, 'g', -1, 64), "UNIFIED", "FUND")
	if err != nil {
		return "", err
	}

	// Send withdraw request from FUND account to master wallet
	resultMap, err := c.sendRequest(ctx, map[string]any{
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
	return "", fmt.Errorf("[Bybit API] CreateWithdraw: Invalid response format")
}

// @private createInternalTransfer Create internal account transfer (f.e. from UNIFIED to FUND)
// coin: Currency ticker
// network: Network code
// fromAccountType: Account type (FUND, UNIFIED)
// toAccountType: Account type (UNIFIED, FUND)
// @docs https://bybit-exchange.github.io/docs/v5/asset/transfer/create-inter-transfer
func (c *HTTPClient) createInternalTransfer(ctx context.Context, coin, amount, fromAccountType, toAccountType string) (int, error) {
	resultMap, err := c.sendRequest(ctx, map[string]any{
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
	return 0, fmt.Errorf("[Bybit API] CreateInternalTransfer: Invalid response format")
}

// @private getMinWithdrawalAmount
// Get min withdrawal amount for coin in network
// coin: Currency ticker
// network: Network code
func (c *HTTPClient) getMinWithdrawalAmount(ctx context.Context, coin, network string) (float64, error) {
	result, err := c.GetCoinInfo(ctx, coin, network)
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
	return 0, fmt.Errorf("[Bybit API] getMinWithdrawalAmount: Min withdrawal amount not found for coin '%s' on network '%s'",
		coin, network)
}

// @private sendRequest
// Send request to Bybit API and return response result or error
// params: Params data
// methodName: Called request method name
func (c *HTTPClient) sendRequest(ctx context.Context, params map[string]any, methodName string) (map[string]any, error) {
	// Create new request entity
	reqClient := c.bybit.NewUtaBybitServiceWithParams(params)

	// Get method by name (by reflection)
	v := reflect.ValueOf(reqClient)
	method := v.MethodByName(methodName)
	if !method.IsValid() || method.Kind() != reflect.Func {
		return nil, fmt.Errorf("[Bybit API] sendRequest: Invalid request method: %s", methodName)
	}

	// Call method
	args := []reflect.Value{reflect.ValueOf(ctx)}
	callRes := method.Call(args)

	// Check method call result
	if len(callRes) != 2 {
		return nil, fmt.Errorf("[Bybit API] sendRequest: Method %s returned unexpected response", methodName)
	}
	responseVal := callRes[0]
	if !responseVal.IsValid() || responseVal.IsNil() {
		return nil, fmt.Errorf("[Bybit API] sendRequest: Method %s returned invalid or nil response", methodName)
	}
	serverResponse, ok := responseVal.Interface().(*bybitApi.ServerResponse)
	if !ok {
		return nil, fmt.Errorf("[Bybit API] sendRequest: Method %s returned unexpected response type: %T",
			methodName, responseVal.Interface())
	}

	// Check method call response for errors
	errorVal := callRes[1]
	if errorVal.IsValid() && !errorVal.IsNil() {
		if serverError, ok := errorVal.Interface().(error); ok && serverError != nil {
			return nil, fmt.Errorf("[Bybit API] sendRequest: HTTP request error for method %s: %w",
				methodName, serverError)
		} else if !ok {
			return nil, fmt.Errorf("[Bybit API] sendRequest: Method %s returned invalid error type: %T",
				methodName, errorVal.Interface())
		}
	}

	// Check API response
	if serverResponse.RetCode != 0 {
		return nil, fmt.Errorf("[Bybit API] sendRequest: API error for method %s (code: %d): %s",
			methodName, serverResponse.RetCode, serverResponse.RetMsg)
	}

	// Extract response
	if serverResponse.Result == nil {
		return make(map[string]any), nil
	}
	result, ok := serverResponse.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("[Bybit API] sendRequest: Method %s returned unexpected result type: %T",
			methodName, serverResponse.Result)
	}

	return result, nil
}
