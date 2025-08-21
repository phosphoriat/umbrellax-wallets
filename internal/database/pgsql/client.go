package pgsql

import (
	"fmt"
	"log"
	"sync"
	"time"

	"wallets/internal/config"
	"wallets/internal/models"

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

		if err == nil {
			// Apply models auto-migrations once, if needed
			if migErr := RunPgSQLMigrations(); migErr != nil {
				log.Printf("Failed to run PostgreSQL migrations: %v", migErr)
			}
		}
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
	// Form DSN pgsqlConnection configuration string
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=UTC",
		cfg.DbHost, cfg.DbUser, cfg.DbPassword, cfg.DbName, cfg.DbPort, cfg.DbSslMode)

	// Open DB PostgreSQL connection
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get GORM DB definition
	sqlDb, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	// Set pgsqlConnection pool settings
	sqlDb.SetMaxIdleConns(10)
	sqlDb.SetMaxOpenConns(100)
	sqlDb.SetConnMaxLifetime(time.Hour)

	// Check pgsqlConnection
	if err := sqlDb.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	fmt.Println("Database pgsqlConnection established")

	return &Client{
		db: db,
	}, nil
}

// RunPgSQLMigrations Apply auto-migrations once, if needed
func RunPgSQLMigrations() error {
	if clientInstance == nil {
		return fmt.Errorf("database connection is not initialized")
	}

	// Get migrator instance
	migrator := clientInstance.db.Migrator()

	// Define models list for migrations
	modelsList := []struct {
		model interface{}
		name  string
	}{
		{&models.Action{}, "actions"},
	}

	// Check if tables exists and apply migrations through model instances
	for _, m := range modelsList {
		if !migrator.HasTable(m.model) {
			log.Printf("Creating %s table...", m.name)
			if err := clientInstance.db.AutoMigrate(m.model); err != nil {
				return fmt.Errorf("failed to migrate %s table: %w", m.name, err)
			}
			log.Printf("%s table created successfully", m.name)

		} else {
			log.Printf("%s table already exists, skipping migration", m.name)
		}
	}

	return nil
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
