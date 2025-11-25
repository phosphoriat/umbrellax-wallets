package aml

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"wallets/internal/config"

	"github.com/avast/retry-go/v5"
)

// Client Bitok HTTP client struct
type Client struct {
	apiUrl     string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

// Global package vars
var (
	clientInstance *Client
	once           sync.Once
)

// GetClient Get global Bitok Client instance
func GetClient() *Client {
	once.Do(func() {
		clientInstance = newBitokClient()
	})
	return clientInstance
}

// @private newBitokClient Create new Client instance (private constructor)
func newBitokClient() *Client {
	// Load config
	cfg, _ := config.Load()

	return &Client{
		apiUrl:    cfg.AmlApiUrl,
		apiKey:    cfg.AmlApiKey,
		apiSecret: cfg.AmlApiSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetManualCheck Get manual check info
// checkId: Manual check id
func (c *Client) GetManualCheck(checkId string) (string, string, error) {
	timestamp := time.Now().UnixMilli()
	endpoint := "/v1/manual-checks/" + checkId

	req, err := http.NewRequest("GET", c.apiUrl+endpoint, nil)
	if err != nil {
		return "", "", fmt.Errorf("API request create error: %w", err)
	}

	signature := c.getHMAC("GET", endpoint, timestamp, "")
	c.setHeaders(req, timestamp, signature)

	res, err := c.executeRequest(req)
	if err != nil {
		return "", "", err
	}

	_, okI := res["id"].(string)
	status, okC := res["check_status"].(string)
	riskLevel, okR := res["risk_level"].(string)
	if !okI || !okC || !okR {
		return "", "", fmt.Errorf("API response format error")
	}

	return status, riskLevel, nil
}

// CheckAddress Manual check address
// network: Network native token
// address: Address
func (c *Client) CheckAddress(network, address string) (string, string, error) {
	// Get native token
	nativeToken, err := config.GetChainSetting[string](config.NetworkSettings, fmt.Sprintf("%s.native_token", network))
	if err != nil {
		return "", "", fmt.Errorf("failed to get native_token for %s from config: %w", network, err)
	}

	timestamp := time.Now().UnixMilli()
	endpoint := "/v1/manual-checks/check-address/"

	payload, err := json.Marshal(map[string]any{
		"network": nativeToken,
		"address": address,
	})
	if err != nil {
		return "", "", fmt.Errorf("JSON serialize error: %w", err)
	}

	req, err := http.NewRequest("POST", c.apiUrl+endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", fmt.Errorf("API request create error: %w", err)
	}

	signature := c.getHMAC("POST", endpoint, timestamp, string(payload))
	c.setHeaders(req, timestamp, signature)

	res, err := c.executeRequest(req)
	if err != nil {
		return "", "", err
	}

	amlId, ok := res["id"].(string)
	if !ok {
		return "", "", fmt.Errorf("API response format error: id not found")
	}

	var status, riskLevel string

	// Create retrier with options
	retrier := retry.New(
		retry.Attempts(30),
		retry.Delay(1*time.Second),
		retry.DelayType(retry.FixedDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Printf("CheckAddress retry attempt %d: %v", n, err)
		}),
	)

	err = retrier.Do(
		func() error {
			var err error
			status, riskLevel, err = c.GetManualCheck(amlId)
			if err != nil {
				return err
			}

			if status == "error" {
				return retry.Unrecoverable(fmt.Errorf("status=error"))
			}

			if status == "checked" && riskLevel != "undefined" {
				return nil
			}

			return fmt.Errorf("status=%s", status)
		},
	)
	if err != nil {
		return amlId, "", fmt.Errorf("AML address check '%s' failed: %w", amlId, err)
	}

	return amlId, riskLevel, nil
}

// CheckTransaction Manual check transaction
// network: Network native token
// currency: Token ticker
// hash: Transaction hash
// toAddress: Transaction to address
// direction: incoming/outgoing
func (c *Client) CheckTransaction(network, currency, hash, toAddress, direction string) (string, error) {
	// Get native token
	nativeToken, err := config.GetChainSetting[string](config.NetworkSettings, fmt.Sprintf("%s.native_token", network))
	if err != nil {
		return "", fmt.Errorf("failed to get native_token for %s from config: %w", network, err)
	}

	// Get token id
	var tokenId string
	if currency == nativeToken {
		tokenId = "native"
	} else {
		tokenId, err = config.GetChainSetting[string](config.ContractsAddresses, fmt.Sprintf("%s.%s", network, currency))
		if err != nil {
			return "", fmt.Errorf("failed to get contract address: %w", err)
		}
	}

	timestamp := time.Now().UnixMilli()
	endpoint := "/v1/manual-checks/check-transfer"

	payload, err := json.Marshal(map[string]any{
		"network":        nativeToken,
		"token_id":       tokenId,
		"tx_hash":        hash,
		"output_address": toAddress,
		"direction":      direction,
	})
	if err != nil {
		return "", fmt.Errorf("JSON serialize error: %w", err)
	}

	req, err := http.NewRequest("POST", c.apiUrl+endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("API request create error: %w", err)
	}

	signature := c.getHMAC("POST", endpoint, timestamp, string(payload))
	c.setHeaders(req, timestamp, signature)

	res, err := c.executeRequest(req)
	if err != nil {
		return "", err
	}

	amlId, ok := res["id"].(string)
	if !ok {
		return "", fmt.Errorf("API response format error: id not found")
	}

	return amlId, nil
}

// @private executeRequest
// Execute HTTP request and parse response
func (c *Client) executeRequest(req *http.Request) (map[string]any, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("API response read error: %w", err)
	}

	// Check response status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API response body error: %s", string(body))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("API response JSON parse error: %w", err)
	}

	return result, nil
}

// @private setHeaders
// Set request headers
func (c *Client) setHeaders(req *http.Request, timestamp int64, signature string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("API-KEY-ID", c.apiKey)
	req.Header.Set("API-TIMESTAMP", strconv.FormatInt(timestamp, 10))
	req.Header.Set("API-SIGNATURE", signature)
}

// @private getHMAC
// Generate HMAC signature
func (c *Client) getHMAC(method, endpoint string, timestamp int64, jsonPayload string) string {
	strToSign := fmt.Sprintf("%s\n%s\n%d", strings.ToUpper(method), endpoint, timestamp)
	if jsonPayload != "" {
		strToSign += "\n" + jsonPayload
	}

	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(strToSign))
	signature := h.Sum(nil)

	return base64.StdEncoding.EncodeToString(signature)
}
