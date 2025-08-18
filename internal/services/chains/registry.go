package chains

import (
	"fmt"
	"sync"
)

// ServiceRegistry Stores all available chain services
type ServiceRegistry struct {
	services map[string]func() ChainService
	mu       sync.RWMutex
}

var registry = &ServiceRegistry{
	services: make(map[string]func() ChainService),
}

// RegisterChainService Register new chain service
func RegisterChainService(name string, factory func() ChainService) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.services[name] = factory
}

// GetChainService Get chain service from registry by network
func GetChainService(network string) (ChainService, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	factory, exists := registry.services[network]
	if !exists {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	return factory(), nil
}

// GetAvailableNetworks Get list of registered networks
func GetAvailableNetworks() []string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	networks := make([]string, 0, len(registry.services))
	for network := range registry.services {
		networks = append(networks, network)
	}

	return networks
}

// init Register chain services (when package will import in main.go)
func init() {
	RegisterChainService("tron", func() ChainService { return GetTron() })
	RegisterChainService("ethereum", func() ChainService { return GetEthereum() })
	RegisterChainService("ton", func() ChainService { return GetTon() })
	RegisterChainService("bitcoin", func() ChainService { return GetBitcoin() })
	RegisterChainService("solana", func() ChainService { return GetSolana() })
}
