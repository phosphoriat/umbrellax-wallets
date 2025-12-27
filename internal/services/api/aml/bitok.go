package aml

import (
	"bytes"
	"context"
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
			Timeout: 5 * time.Second,
		},
	}
}

// GetManualCheck Get manual check info
// ctx: Current process context
// checkId: Manual check id
// Returns: Check status, risk level
func (c *Client) GetManualCheck(ctx context.Context, checkId string) (string, string, error) {
	// Execute request and get parsed response
	res, err := c.executeRequest(ctx, http.MethodGet, "/v1/manual-checks/"+checkId, nil)
	if err != nil {
		return "", "", err
	}

	// Get amd id, status and risk level from parsed response
	_, okI := res["id"].(string)
	status, okC := res["check_status"].(string)
	riskLevel, okR := res["risk_level"].(string)
	if !okI || !okC || !okR {
		return "", "", fmt.Errorf("[Bitok API] Response format error")
	}

	return status, riskLevel, nil
}

// CheckAddress Manual check address
// ctx: Current process context
// network: Network native token
// address: Address
// Returns: Check status, risk level
func (c *Client) CheckAddress(ctx context.Context, network, address string) (string, string, error) {
	// Get native token for network
	nativeToken := config.NetworkSettings[network].NativeToken

	// Execute request and get parsed response
	res, err := c.executeRequest(ctx, http.MethodPost, "/v1/manual-checks/check-address/", map[string]any{
		"network": nativeToken,
		"address": address,
	})
	if err != nil {
		return "", "", err
	}

	// Get aml id from parsed response
	amlId, ok := res["id"].(string)
	if !ok {
		return "", "", fmt.Errorf("[Bitok API] Response format error: id not found")
	}

	// Create retrier with options
	retrier := retry.New(
		retry.Attempts(30),
		retry.Delay(1*time.Second),
		retry.DelayType(retry.FixedDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Printf("[Bitok API] CheckAddress retry attempt %d: %v", n, err)
		}),
	)

	// Run manual check with retries
	var status, riskLevel string
	err = retrier.Do(
		func() error {
			var err error
			status, riskLevel, err = c.GetManualCheck(ctx, amlId)
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
		return amlId, "", fmt.Errorf("[Bitok API] AML address check '%s' failed: %w", amlId, err)
	}

	return amlId, riskLevel, nil
}

// CheckTransaction Manual check transaction
// ctx: Current process context
// network: Network native token
// currency: Token ticker
// hash: Transaction hash
// toAddress: Transaction to address
// direction: incoming/outgoing
// Returns: Aml id
func (c *Client) CheckTransaction(ctx context.Context, network, currency, hash, toAddress, direction string) (string, error) {
	// Get native token for network
	nativeToken := config.NetworkSettings[network].NativeToken

	// Get token id value for payload by currency
	tokenId := "native"
	if currency != nativeToken {
		var ok bool
		if tokenId, ok = config.GetContractAddress(network, currency); !ok {
			return "", fmt.Errorf("[Bitok API] Failed to get contract address for %s in %s network from config",
				currency, network)
		}
	}
	// Execute request and get parsed response
	res, err := c.executeRequest(ctx, http.MethodPost, "/v1/manual-checks/check-transfer", map[string]any{
		"network":        nativeToken,
		"token_id":       tokenId,
		"tx_hash":        hash,
		"output_address": toAddress,
		"direction":      direction,
	})
	if err != nil {
		return "", err
	}

	// Get aml id from parsed response
	amlId, ok := res["id"].(string)
	if !ok {
		return "", fmt.Errorf("[Bitok API] Response format error: id not found")
	}

	return amlId, nil
}

// @private executeRequest Create HTTP request entity, execute and return response data
// ctx: Current process context
// method: Request method
// endpoint: API endpoint
// data: Request payload data
func (c *Client) executeRequest(ctx context.Context, method, endpoint string, data map[string]any) (map[string]any, error) {
	// Create request payload
	var (
		payload    *bytes.Reader
		sigPayload string
	)
	if len(data) > 0 {
		parsedData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("[Bitok API] JSON payload serialize error: %w", err)
		}

		payload = bytes.NewReader(parsedData)
		sigPayload = string(parsedData)
	}

	// Create new request entity
	req, err := http.NewRequestWithContext(ctx, method, c.apiUrl+endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("[Bitok API] Failed to create request: %w", err)
	}

	// Add signature to headers
	timestamp := time.Now().UnixMilli()
	signature := c.getHMAC(method, endpoint, timestamp, sigPayload)
	c.setHeaders(req, timestamp, signature)

	// Execute request and get response
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("[Bitok API] Failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("[Bitok API] Response failed with status %d: %s", resp.StatusCode, body)
	}

	// Read and parse response body
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("[Bitok API] Failed to parse response JSON: %w", err)
	}

	return result, nil
}

// @private setHeaders Set request headers
// req: Request entity
// timestamp: Current timestamp
// signature: Request signature
func (c *Client) setHeaders(req *http.Request, timestamp int64, signature string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("API-KEY-ID", c.apiKey)
	req.Header.Set("API-TIMESTAMP", strconv.FormatInt(timestamp, 10))
	req.Header.Set("API-SIGNATURE", signature)
}

// @private getHMAC Generate HMAC signature
// method: Request method
// endpoint: API endpoint
// timestamp: Current timestamp
// jsonPayload: Request payload in JSON
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
