package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"wallets/internal/config"

	"github.com/hashicorp/vault/api"
)

// Global Vault connection var
var vaultClient *api.Client

// InitVault Init connection to Vault
func InitVault(cfg *config.Config) error {
	// Configure Vault client
	vaultConfig := api.DefaultConfig()
	vaultConfig.Address = fmt.Sprintf("%s://%s:%s", cfg.VaultScheme, cfg.VaultHost, cfg.VaultPort)
	vaultConfig.Timeout = 10 * time.Second

	// Create Vault client
	client, err := api.NewClient(vaultConfig)
	if err != nil {
		return fmt.Errorf("failed to create Vault client: %w", err)
	}

	// Set authentication token, if defined in config
	if cfg.VaultToken != "" {
		client.SetToken(cfg.VaultToken)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	health, err := client.Sys().HealthWithContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to check Vault health: %w", err)
	}
	if !health.Initialized {
		return fmt.Errorf("vault is not initialized")
	}
	if health.Sealed {
		return fmt.Errorf("vault is sealed")
	}

	// Assign client to global variable
	vaultClient = client
	log.Printf("Vault connection established: %s://%s:%s (Version: %s)",
		cfg.VaultScheme, cfg.VaultHost, cfg.VaultPort, health.Version)

	// Auth using AppRole, if defined in config
	if cfg.VaultRoleId != "" && cfg.VaultSecretId != "" {
		if err := AuthWithAppRole(cfg.VaultRoleId, cfg.VaultSecretId); err != nil {
			return fmt.Errorf("failed to auth with AppRole: %w", err)
		}
		log.Println("Successfully auth with Vault using AppRole")
	}

	return nil
}

// GetVaultClient Return Vault client instance
func GetVaultClient() *api.Client {
	return vaultClient
}

// CloseVault Close Vault connection (cleanup)
func CloseVault() error {
	if vaultClient != nil {
		// Vault client doesn't need explicit closing, but we can clear the token
		vaultClient.ClearToken()
		vaultClient = nil
	}

	return nil
}

// AuthWithAppRole Auth using AppRole method
func AuthWithAppRole(roleID, secretID string) error {
	if vaultClient == nil {
		return fmt.Errorf("vault client not initialized")
	}

	// Prepare AppRole authentication data
	data := map[string]interface{}{
		"role_id":   roleID,
		"secret_id": secretID,
	}

	// Try to auth
	resp, err := vaultClient.Logical().Write("auth/approle/login", data)
	if err != nil {
		return fmt.Errorf("AppRole authentication failed: %w", err)
	}
	if resp == nil || resp.Auth == nil {
		return fmt.Errorf("AppRole authentication returned empty response")
	}

	// Set the token from the response
	vaultClient.SetToken(resp.Auth.ClientToken)

	return nil
}

// GetSecret Get secret from Vault KV store
func GetSecret(path string) (map[string]interface{}, error) {
	if vaultClient == nil {
		return nil, fmt.Errorf("vault client not initialized")
	}

	secret, err := vaultClient.Logical().Read(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret from %s: %w", path, err)
	}

	if secret == nil {
		return nil, fmt.Errorf("secret not found at path: %s", path)
	}

	return secret.Data, nil
}

// GetSecretValue Get specific value from secret
func GetSecretValue(path, key string) (interface{}, error) {
	secretData, err := GetSecret(path)
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
func SetSecret(path string, data map[string]interface{}) error {
	if vaultClient == nil {
		return fmt.Errorf("vault client not initialized")
	}

	// For KV v2, we need to wrap data under "data" key
	wrappedData := map[string]interface{}{
		"data": data,
	}

	_, err := vaultClient.Logical().Write(path, wrappedData)
	if err != nil {
		return fmt.Errorf("failed to write secret to %s: %w", path, err)
	}

	return nil
}

// DeleteSecret Delete secret from Vault
func DeleteSecret(path string) error {
	if vaultClient == nil {
		return fmt.Errorf("vault client not initialized")
	}

	_, err := vaultClient.Logical().Delete(path)
	if err != nil {
		return fmt.Errorf("failed to delete secret at %s: %w", path, err)
	}

	return nil
}

// ListSecrets List secrets at given path
func ListSecrets(path string) ([]interface{}, error) {
	if vaultClient == nil {
		return nil, fmt.Errorf("vault client not initialized")
	}

	secret, err := vaultClient.Logical().List(path)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets at %s: %w", path, err)
	}

	if secret == nil || secret.Data == nil {
		return []interface{}{}, nil
	}

	if keys, ok := secret.Data["keys"].([]interface{}); ok {
		return keys, nil
	}

	return []interface{}{}, nil
}
