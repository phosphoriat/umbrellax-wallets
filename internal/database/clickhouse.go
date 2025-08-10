package database

import (
	"context"
	"fmt"
	"log"
	"wallets/internal/config"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Global ClickHouse connection var
var clickhouseConnection driver.Conn

// InitClickHouse Init connection to ClickHouse
func InitClickHouse(cfg *config.Config) error {
	// Establish connection
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%s", cfg.ClickhouseHost, cfg.ClickhousePort)},
		Auth: clickhouse.Auth{
			Database: cfg.ClickhouseName,
			Username: cfg.ClickhouseUser,
			Password: cfg.ClickhousePassword,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to ClickHouse: %w", err)
	}

	// Test connection
	ctx := context.Background()
	if err := conn.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping ClickHouse: %w", err)
	}

	// Assign connection to global variable
	clickhouseConnection = conn
	log.Printf("ClickHouse connection established: %s@%s:%s/%s",
		cfg.ClickhouseUser, cfg.ClickhouseHost, cfg.ClickhousePort, cfg.ClickhouseName)

	return nil
}

// GetClickhouseConnection Return ClickHouse connection instance
func GetClickhouseConnection() driver.Conn {
	return clickhouseConnection
}

// CloseClickHouseConnection Close ClickHouse connection
func CloseClickHouseConnection() error {
	if clickhouseConnection != nil {
		return clickhouseConnection.Close()
	}

	return nil
}
