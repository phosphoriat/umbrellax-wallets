package aml

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"wallets/internal/config"
)

// Client Bitok HTTP client struct
type Client struct {
	apiUrl     string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
	mu         sync.RWMutex
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
	cfg := config.Load()

	return &Client{
		apiUrl:    cfg.AmlApiUrl,
		apiKey:    cfg.AmlApiKey,
		apiSecret: cfg.AmlApiSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CheckAddress Manual check address
func (c *Client) CheckAddress(network, tokenId, address string) (map[string]interface{}, error) {
	timestamp := time.Now().UnixMilli()
	endpoint := "/v1/manual-checks/check-address/"

	payload, err := json.Marshal(map[string]interface{}{
		"network":  network,
		"token_id": tokenId,
		"address":  address,
	})
	if err != nil {
		return nil, fmt.Errorf("JSON serialize error: %w", err)
	}

	req, err := http.NewRequest("POST", c.apiUrl+endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("request create error: %w", err)
	}

	signature := c.getHMAC("POST", endpoint, timestamp, string(payload))
	c.setHeaders(req, timestamp, signature)

	return c.executeRequest(req)
}

// CheckTransaction Manual check transaction
func (c *Client) CheckTransaction(network, tokenId, hash, toAddress string) (map[string]interface{}, error) {
	timestamp := time.Now().UnixMilli()
	endpoint := "/v1/manual-checks/check-transfer/"

	payload, err := json.Marshal(map[string]interface{}{
		"network":        network,
		"token_id":       tokenId,
		"tx_hash":        hash,
		"output_address": toAddress,
		"direction":      "outcoming",
	})
	if err != nil {
		return nil, fmt.Errorf("JSON serialize error: %w", err)
	}

	req, err := http.NewRequest("POST", c.apiUrl+endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("request create error: %w", err)
	}

	signature := c.getHMAC("POST", endpoint, timestamp, string(payload))
	c.setHeaders(req, timestamp, signature)

	return c.executeRequest(req)
}

// @private executeRequest Execute HTTP request and parse response
func (c *Client) executeRequest(req *http.Request) (map[string]interface{}, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("response read error: %w", err)
	}

	// Check response status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: body: %s", string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("response JSON parse error: %w", err)
	}

	return result, nil
}

// @private setHeaders Set request headers
func (c *Client) setHeaders(req *http.Request, timestamp int64, signature string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("API-KEY-ID", c.apiKey)
	req.Header.Set("API-TIMESTAMP", strconv.FormatInt(timestamp, 10))
	req.Header.Set("API-SIGNATURE", signature)
}

// @private getHMAC Generate HMAC signature
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
