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
	"time"
	"wallets/internal/config"
)

// Client Bitok HTTP client struct
type Client struct {
	config     *config.Config
	httpClient *http.Client
}

// NewAmlClient Create new Client instance
func NewAmlClient(config *config.Config) *Client {
	return &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CheckAddress Manual check address
func (client *Client) CheckAddress(network, tokenId, address string) (map[string]interface{}, error) {
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

	req, err := http.NewRequest("POST", client.config.AmlApiUrl+endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("request create error: %w", err)
	}

	signature := client.getHMAC("POST", endpoint, timestamp, string(payload))
	client.setHeaders(req, timestamp, signature)

	return client.executeRequest(req)
}

// CheckTransaction Manual check transaction
func (client *Client) CheckTransaction(network, tokenId, hash, toAddress, direction string) (map[string]interface{}, error) {
	timestamp := time.Now().UnixMilli()
	endpoint := "/v1/manual-checks/check-transfer/"

	payload, err := json.Marshal(map[string]interface{}{
		"network":        network,
		"token_id":       tokenId,
		"tx_hash":        hash,
		"output_address": toAddress,
		"direction":      direction,
	})
	if err != nil {
		return nil, fmt.Errorf("JSON serialize error: %w", err)
	}

	req, err := http.NewRequest("POST", client.config.AmlApiUrl+endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("request create error: %w", err)
	}

	signature := client.getHMAC("POST", endpoint, timestamp, string(payload))
	client.setHeaders(req, timestamp, signature)

	return client.executeRequest(req)
}

// @private executeRequest Execute HTTP request and parse response
func (client *Client) executeRequest(req *http.Request) (map[string]interface{}, error) {
	resp, err := client.httpClient.Do(req)
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

// @private getHMAC Generate HMAC signature
func (client *Client) getHMAC(method, endpoint string, timestamp int64, jsonPayload string) string {
	strToSign := fmt.Sprintf("%s\n%s\n%d", strings.ToUpper(method), endpoint, timestamp)
	if jsonPayload != "" {
		strToSign += "\n" + jsonPayload
	}

	h := hmac.New(sha256.New, []byte(client.config.AmlApiSecret))
	h.Write([]byte(strToSign))
	signature := h.Sum(nil)

	return base64.StdEncoding.EncodeToString(signature)
}

// @private setHeaders Set request headers
func (client *Client) setHeaders(req *http.Request, timestamp int64, signature string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("API-KEY-ID", client.config.AmlApiKey)
	req.Header.Set("API-TIMESTAMP", strconv.FormatInt(timestamp, 10))
	req.Header.Set("API-SIGNATURE", signature)
}
