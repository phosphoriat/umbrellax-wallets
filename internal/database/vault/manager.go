package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"wallets/internal/config"

	"github.com/hashicorp/vault/api"
)

// Client Vault connection struct
type Client struct {
	vault           *api.Client
	mu              sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	lifetimeWatcher *api.LifetimeWatcher
}

// Package level constants
const (
	KVMount = "kv" // Default mount path for KV v2 secrets engine
)

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

	// Set authentication token
	if cfg.VaultToken != "" {
		vaultClient.SetToken(cfg.VaultToken)
	}

	// Connection test
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

	// Create context for lifecycle management
	clientCtx, clientCancel := context.WithCancel(context.Background())

	client := &Client{
		vault:  vaultClient,
		ctx:    clientCtx,
		cancel: clientCancel,
	}

	// Auth using AppRole
	if cfg.VaultRoleId != "" && cfg.VaultSecretId != "" {
		if err := client.authWithAppRole(cfg.VaultRoleId, cfg.VaultSecretId); err != nil {
			clientCancel()
			return nil, fmt.Errorf("failed to auth with AppRole: %w", err)
		}
		log.Printf("Successfully auth with Vault using AppRole")

		// Start token renewal after successful authentication
		if err := client.startTokenRenewal(); err != nil {
			clientCancel()
			return nil, fmt.Errorf("failed to start token renewal: %w", err)
		}
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

// @private closeConnection Close Vault connection with graceful shutdown
func (c *Client) closeConnection() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Cancel context to stop all goroutines
	if c.cancel != nil {
		c.cancel()
	}

	// Stop token renewal watcher
	if c.lifetimeWatcher != nil {
		c.lifetimeWatcher.Stop()
	}

	if c.vault != nil {
		c.vault.ClearToken()
		c.vault = nil
	}

	log.Printf("Vault connection closed gracefully")
	return nil
}

// GetSecret Get secret from Vault KV v2 store
func (c *Client) GetSecret(path string) (map[string]any, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	fullPath := fmt.Sprintf("%s/data/%s", KVMount, path)

	secret, err := client.Logical().Read(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret from %s: %w", fullPath, err)
	}
	if secret == nil {
		return nil, fmt.Errorf("secret not found at path: %s", fullPath)
	}

	if data, ok := secret.Data["data"].(map[string]any); ok {
		return data, nil
	}
	return nil, fmt.Errorf("invalid secret format at path: %s", fullPath)
}

// SetSecret Set secret in Vault KV v2 store
func (c *Client) SetSecret(path string, data map[string]any) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	fullPath := fmt.Sprintf("%s/data/%s", KVMount, path)

	_, err = client.Logical().Write(fullPath, map[string]any{
		"data": data,
	})
	if err != nil {
		return fmt.Errorf("failed to write secret to %s: %w", fullPath, err)
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
	data := map[string]any{
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

// @private startTokenRenewal Start automatic token renewal using LifetimeWatcher
func (c *Client) startTokenRenewal() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	// Get token info
	secret, err := client.Auth().Token().LookupSelf()
	if err != nil {
		return fmt.Errorf("failed to lookup Vault token info: %w", err)
	}

	// Check if token is renewable
	renewable, ok := secret.Data["renewable"].(bool)
	if !ok || !renewable {
		log.Printf("Vault token is not renewable, skipping auto-renewal setup")
		return nil
	}

	// Get TTL from data
	var increment int
	if ttl, ok := secret.Data["ttl"].(float64); ok {
		increment = int(ttl / 2) // Renew for 1/2 of TTL
	} else if ttl, ok := secret.Data["ttl"].(json.Number); ok {
		ttlInt, _ := ttl.Int64()
		increment = int(ttlInt / 2)
	} else {
		increment = 3600 // Fallback to 1 hour
	}

	// Making RenewSelf to get secret with filled Auth
	renewedSecret, err := client.Auth().Token().RenewSelf(increment)
	if err != nil {
		return fmt.Errorf("failed to renew Vault token initially: %w", err)
	}

	// Now we use renewedSecret, which has Auth filled in
	watcher, err := client.NewLifetimeWatcher(&api.LifetimeWatcherInput{
		Secret:    renewedSecret,
		Increment: increment,
	})
	if err != nil {
		return fmt.Errorf("failed to create Vault token watcher: %w", err)
	}

	c.lifetimeWatcher = watcher

	// Run watcher in separate goroutine
	go watcher.Start()

	// Process token update events in separate goroutine
	go func() {
		for {
			select {
			case err := <-watcher.DoneCh():
				if err != nil {
					log.Printf("Vault token renewal failed: %v", err)
				}
				// Token reached maximum TTL, reauthentication required
				log.Printf("Vault token renewal stopped, re-authentication may be required")
				return

			case renewal := <-watcher.RenewCh():
				log.Printf("Vault token successfully renewed. Remaining TTL: %d seconds",
					renewal.Secret.Auth.LeaseDuration)

			case <-c.ctx.Done():
				// Graceful shutdown
				log.Printf("Vault token renewal stopped due to context cancellation")
				return
			}
		}
	}()

	log.Printf("Vault token auto-renewal started with increment: %d seconds", increment)
	return nil
}
