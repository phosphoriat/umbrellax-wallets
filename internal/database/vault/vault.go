package vault

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"wallets/internal/config"

	"github.com/hashicorp/vault/api"
)

// Client Vault connection struct
type Client struct {
	vault *api.Client
	mu    sync.RWMutex
}

// Global package vars
var (
	clientInstance *Client
	once           sync.Once
)

// GetClient Get global Vault Client instance
func GetClient() *Client {
	return clientInstance
}

// Init Vault connection (singleton)
func Init(cfg *config.Config) error {
	var err error
	once.Do(func() {
		clientInstance, err = newVaultClient(cfg)
	})
	return err
}

// Close global Redis connection
func Close() error {
	if clientInstance == nil {
		return nil
	}
	return clientInstance.closeConnection()
}

// @private newVaultClient Create new Vault Client instance (private constructor)
func newVaultClient(cfg *config.Config) (*Client, error) {
	// Configure Vault client
	vaultConfig := api.DefaultConfig()
	vaultConfig.Address = fmt.Sprintf("%s://%s:%s", cfg.VaultScheme, cfg.VaultHost, cfg.VaultPort)
	vaultConfig.Timeout = 10 * time.Second

	// Create Vault client
	vaultClient, err := api.NewClient(vaultConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Vault client: %w", err)
	}

	// Set authentication token, if defined in config
	if cfg.VaultToken != "" {
		vaultClient.SetToken(cfg.VaultToken)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	health, err := vaultClient.Sys().HealthWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check Vault health: %w", err)
	}
	if !health.Initialized {
		return nil, fmt.Errorf("vault is not initialized")
	}
	if health.Sealed {
		return nil, fmt.Errorf("vault is sealed")
	}

	log.Printf("Vault connection established: %s://%s:%s (Version: %s)",
		cfg.VaultScheme, cfg.VaultHost, cfg.VaultPort, health.Version)

	client := &Client{
		vault: vaultClient,
	}

	// Auth using AppRole, if defined in config
	if cfg.VaultRoleId != "" && cfg.VaultSecretId != "" {
		if err := client.authWithAppRole(cfg.VaultRoleId, cfg.VaultSecretId); err != nil {
			return nil, fmt.Errorf("failed to auth with AppRole: %w", err)
		}
		log.Printf("Successfully auth with Vault using AppRole")
	}

	return client, nil
}

// @private getClient Get Vault client instance
func (c *Client) getClient() (*api.Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.vault == nil {
		return nil, fmt.Errorf("vault client is not initialized")
	}

	return c.vault, nil
}

// @private closeConnection Close Vault connection
func (c *Client) closeConnection() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.vault != nil {
		// Vault client doesn't need explicit closing, but we can clear the token
		c.vault.ClearToken()
		c.vault = nil
	}

	return nil
}

// GetSecret Get secret from Vault KV store
func (c *Client) GetSecret(path string) (map[string]interface{}, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	secret, err := client.Logical().Read(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret from %s: %w", path, err)
	}

	if secret == nil {
		return nil, fmt.Errorf("secret not found at path: %s", path)
	}

	return secret.Data, nil
}

// GetSecretValue Get specific value from secret
func (c *Client) GetSecretValue(path, key string) (interface{}, error) {
	secretData, err := c.GetSecret(path)
	if err != nil {
		return nil, err
	}

	// For KV v2, data is nested under "data" key
	if data, ok := secretData["data"].(map[string]interface{}); ok {
		if value, exists := data[key]; exists {
			return value, nil
		}
		return nil, fmt.Errorf("key '%s' not found in secret", key)
	}

	// For KV v1, data is directly accessible
	if value, exists := secretData[key]; exists {
		return value, nil
	}

	return nil, fmt.Errorf("key '%s' not found in secret", key)
}

// SetSecret Set secret in Vault KV store
func (c *Client) SetSecret(path string, data map[string]interface{}) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	// For KV v2, we need to wrap data under "data" key
	wrappedData := map[string]interface{}{
		"data": data,
	}

	_, err = client.Logical().Write(path, wrappedData)
	if err != nil {
		return fmt.Errorf("failed to write secret to %s: %w", path, err)
	}

	return nil
}

// @private authWithAppRole Auth using AppRole method
func (c *Client) authWithAppRole(roleId string, secretId string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	// Prepare AppRole authentication data
	data := map[string]interface{}{
		"role_id":   roleId,
		"secret_id": secretId,
	}

	// Try to auth
	resp, err := client.Logical().Write("auth/approle/login", data)
	if err != nil {
		return fmt.Errorf("AppRole authentication failed: %w", err)
	}
	if resp == nil || resp.Auth == nil {
		return fmt.Errorf("AppRole authentication returned empty response")
	}

	// Set the token from the response
	client.SetToken(resp.Auth.ClientToken)

	return nil
}
