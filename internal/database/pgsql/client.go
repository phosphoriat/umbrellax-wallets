package pgsql

import (
	"fmt"
	"sync"
	"time"

	"wallets/internal/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Client PostgreSQL connection struct
type Client struct {
	db *gorm.DB
	mu sync.RWMutex
}

// Global package vars
var (
	clientInstance *Client
	once           sync.Once
)

// GetDB Get global PostgreSQL Client instance
func GetDB() *gorm.DB {
	if clientInstance == nil {
		return nil
	}
	return clientInstance.db
}

// Init PostgreSQL connection (singleton)
func Init(cfg *config.Config) error {
	var err error
	once.Do(func() {
		clientInstance, err = newPgSQLClient(cfg)
	})
	return err
}

// Close global PostgreSQL connection
func Close() error {
	if clientInstance == nil {
		return nil
	}
	return clientInstance.closeConnection()
}

// @private NewPgSQLClient Create new PostgreSQL Client instance (private constructor)
func newPgSQLClient(cfg *config.Config) (*Client, error) {
	// Form DSN PostgreSQL connection configuration string
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.DbHost, cfg.DbUser, cfg.DbPassword, cfg.DbName, cfg.DbPort, cfg.DbSslMode)

	// Open PostgreSQL connection
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	// Get GORM DB definition
	sqlDb, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Set PostgreSQL connection pool settings
	sqlDb.SetMaxIdleConns(10)
	sqlDb.SetMaxOpenConns(100)
	sqlDb.SetConnMaxLifetime(time.Hour)
	sqlDb.SetConnMaxIdleTime(10 * time.Minute)

	// Check connection
	if err := sqlDb.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}
	fmt.Println("Database pgsqlConnection established successfully")

	return &Client{
		db: db,
	}, nil
}

// @private closeConnection Close PostgreSQL connection
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
