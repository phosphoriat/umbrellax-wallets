package clickhouse

import (
	"context"
	"fmt"
	"log"
	"sync"

	"wallets/internal/config"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Client ClickHouse connection struct
type Client struct {
	conn driver.Conn
	mu   sync.RWMutex
}

// Global package vars
var (
	clientInstance *Client
	once           sync.Once
)

// GetClient Get ClickHouse connection instance
func GetClient() driver.Conn {
	if clientInstance == nil {
		return nil
	}
	return clientInstance.conn
}

// Init ClickHouse connection (singleton)
func Init(cfg *config.Config) error {
	var err error
	once.Do(func() {
		clientInstance, err = newClickHouseClient(cfg)
	})
	return err
}

// Close global ClickHouse connection
func Close() error {
	if clientInstance == nil {
		return nil
	}
	return clientInstance.closeConnection()
}

// @private newClickHouseClient Create new ClickHouse Client instance
func newClickHouseClient(cfg *config.Config) (*Client, error) {
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
		return nil, fmt.Errorf("failed to connect to ClickHouse: %w", err)
	}

	// Test connection
	if err := conn.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping ClickHouse: %w", err)
	}

	log.Printf("ClickHouse connection established: %s@%s:%s/%s",
		cfg.ClickhouseUser, cfg.ClickhouseHost, cfg.ClickhousePort, cfg.ClickhouseName)

	return &Client{
		conn: conn,
	}, nil
}

// @private closeConnection Close ClickHouse connection
func (c *Client) closeConnection() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}

	return nil
}
