package database

import (
	"fmt"
	"log"
	"time"
	"wallets/internal/config"
	"wallets/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Global pgsqlConnection var
var pgsqlConnection *gorm.DB

// InitPgSQL Init pgsqlConnection to PostgreSQL
func InitPgSQL(cfg *config.Config) error {
	// Form DSN pgsqlConnection configuration string
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=UTC",
		cfg.DbHost, cfg.DbUser, cfg.DbPassword, cfg.DbName, cfg.DbPort, cfg.DbSSLMode)

	// Open DB pgsqlConnection
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get GORM DB definition
	sqlDb, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	// Set pgsqlConnection pool settings
	sqlDb.SetMaxIdleConns(10)
	sqlDb.SetMaxOpenConns(100)
	sqlDb.SetConnMaxLifetime(time.Hour)

	// Check pgsqlConnection
	if err := sqlDb.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// Assign pgsqlConnection instance to global pgsqlConnection var
	pgsqlConnection = db
	fmt.Println("Database pgsqlConnection established")

	// Auto-migrate models (if needed)
	if err := RunPgSQLMigrations(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

// GetPgSQLConnection Return database pgsqlConnection instance
func GetPgSQLConnection() *gorm.DB {
	return pgsqlConnection
}

// ClosePgSQLConnection Close database pgsqlConnection
func ClosePgSQLConnection() error {
	if pgsqlConnection != nil {
		sqlDb, err := pgsqlConnection.DB()
		if err != nil {
			return err
		}

		return sqlDb.Close()
	}

	return nil
}

// RunPgSQLMigrations Apply auto-migrations once, if needed
func RunPgSQLMigrations() error {
	if pgsqlConnection == nil {
		return fmt.Errorf("database not initialized")
	}

	// Get migrator instance
	migrator := pgsqlConnection.Migrator()

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
			if err := pgsqlConnection.AutoMigrate(m.model); err != nil {
				return fmt.Errorf("failed to migrate %s table: %w", m.name, err)
			}
			log.Printf("%s table created successfully", m.name)

		} else {
			log.Printf("%s table already exists, skipping migration", m.name)
		}
	}

	return nil
}
