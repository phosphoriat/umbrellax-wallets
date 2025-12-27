package rates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"wallets/internal/config"
)

// Client Rates HTTP client struct
type Client struct {
	apiUrl     string
	httpClient *http.Client
	rates      map[string]any
	mu         sync.RWMutex
	cancel     context.CancelFunc
}

// Global package vars
var (
	clientInstance *Client
	once           sync.Once
)

// GetClient Get global rates Client instance
func GetClient() *Client {
	once.Do(func() {
		clientInstance = newRatesClient()
	})
	return clientInstance
}

// @private newRatesClient Create new Client instance (private constructor)
func newRatesClient() *Client {
	// Load config
	cfg, _ := config.Load()

	return &Client{
		apiUrl: cfg.RatesApiUrl,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		rates: make(map[string]any),
	}
}

// GetRates Get actual rates data from cache
func (c *Client) GetRates() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rates
}

// GetRate Get specific rate value
// @example: GetRate("crypto", "BTC", "USD") -> 87406.49
// @example: GetRate("fiat", "USD", "EUR") -> 0.849365
func (c *Client) GetRate(keys ...string) (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var current any = c.rates
	for _, key := range keys {
		if m, ok := current.(map[string]any); ok {
			current = m[key]
		} else {
			return 0, false
		}
	}

	if rate, ok := current.(float64); ok {
		return rate, true
	}
	return 0, false
}

// Start background rates updater
func (c *Client) Start(ctx context.Context) {
	// Create cancellable context
	ctx, c.cancel = context.WithCancel(ctx)

	// Initial fetch
	if err := c.updateRates(ctx); err != nil {
		log.Printf("[Rates API] Initial fetch failed: %v", err)
	}

	// Start background updater in separate goroutine
	go c.runUpdater(ctx)
}

// Stop background rates updater
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// @private runUpdater Run background update (every 1 min)
func (c *Client) runUpdater(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Rates API] Background updater stopped")
			return

		case <-ticker.C:
			if err := c.updateRates(ctx); err != nil {
				log.Printf("[Rates API] Update failed: %v", err)
			}
		}
	}
}

// @private updateRates Fetch and update rates
func (c *Client) updateRates(ctx context.Context) error {
	rates, err := c.fetchRates(ctx)
	if err != nil {
		return err
	}

	// Extract rates.data
	ratesObj, ok := rates["rates"].(map[string]any)
	if !ok {
		return fmt.Errorf("[Rates API] Invalid response format: 'rates' not found")
	}
	data, ok := ratesObj["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("[Rates API] Invalid response format: 'rates.data' not found")
	}

	c.mu.Lock()
	c.rates = data
	c.mu.Unlock()

	log.Println("[Rates API] Rates updated successfully")
	return nil
}

// @private fetchRates Fetch rates from API
func (c *Client) fetchRates(ctx context.Context) (map[string]any, error) {
	// Create new request entity
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("[Rates API] Failed to create request: %w", err)
	}

	// Add headers
	req.Header.Set("Accept", "application/json")

	// Execute request and get response
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("[Rates API] Failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("[Rates API] Response failed with status %d: %s", resp.StatusCode, body)
	}

	// Read and parse response body
	var rates map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rates); err != nil {
		return nil, fmt.Errorf("[Rates API] Failed to decode response: %w", err)
	}

	return rates, nil
}
