package database

import (
	"log"

	"wallets/internal/config"
)

// InitDB Initialize all connections
func InitDB(cfg *config.Config) error {
	// Init PostgreSQL database connection
	if err := InitPgSQL(cfg); err != nil {
		log.Fatal("Failed to initialize PostgreSQL:", err)
	}
	defer func() {
		if err := ClosePgSQLConnection(); err != nil {
			log.Printf("Error closing PostgreSQL: %v", err)
		}
	}()

	// Init ClickHouse database connection
	if err := InitClickHouse(cfg); err != nil {
		log.Fatal("Failed to initialize ClickHouse:", err)
	}
	defer func() {
		if err := CloseClickHouseConnection(); err != nil {
			log.Printf("Error closing ClickHouse: %v", err)
		}
	}()

	// Init Redis connection
	if err := InitRedis(cfg); err != nil {
		log.Fatal("Failed to initialize Redis:", err)
	}
	defer func() {
		if err := CloseRedis(); err != nil {
			log.Printf("Error closing Redis: %v", err)
		}
	}()

	// Init Vault connection
	if err := InitVault(cfg); err != nil {
		log.Fatal("Failed to initialize Vault:", err)
	}
	defer func() {
		if err := CloseVault(); err != nil {
			log.Printf("Error closing Vault: %v", err)
		}
	}()

	return nil
}
