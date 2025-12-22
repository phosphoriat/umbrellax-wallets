package clickhouse

import (
	"fmt"
	"sync"
	"time"

	"wallets/internal/config"

	"gorm.io/driver/clickhouse"
	"gorm.io/gorm"
)

// Client ClickHouse connection struct
type Client struct {
	db *gorm.DB
	mu sync.RWMutex
}

// Global package vars
var (
	clientInstance *Client
	once           sync.Once
)

// GetDB Get global ClickHouse Client instance
func GetDB() *gorm.DB {
	if clientInstance == nil {
		return nil
	}
	return clientInstance.db
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

// @private newClickHouseClient Create new ClickHouse Client instance (private constructor)
func newClickHouseClient(cfg *config.Config) (*Client, error) {
	// Form DSN ClickHouse connection configuration string
	dsn := fmt.Sprintf("tcp://%s:%s?database=%s&username=%s&password=%s&read_timeout=10s",
		cfg.ClickHouseHost, cfg.ClickHousePort, cfg.ClickHouseName, cfg.ClickHouseUser, cfg.ClickHousePassword)

	// Open ClickHouse connection
	db, err := gorm.Open(clickhouse.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ClickHouse: %w", err)
	}

	// Get GORM DB definition
	sqlDb, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Set ClickHouse connection pool settings
	sqlDb.SetMaxIdleConns(5)
	sqlDb.SetMaxOpenConns(25)
	sqlDb.SetConnMaxLifetime(5 * time.Minute)
	sqlDb.SetConnMaxIdleTime(30 * time.Second)

	// Check connection
	if err := sqlDb.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping ClickHouse: %w", err)
	}
	fmt.Println("ClickHouse connection established successfully")

	return &Client{
		db: db,
	}, nil
}

// @private closeConnection Close ClickHouse connection
func (c *Client) closeConnection() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.db != nil {
		sqlDB, err := c.db.DB()
		if err != nil {
			return err
		}

		err = sqlDB.Close()
		c.db = nil
		return err
	}

	return nil
}
