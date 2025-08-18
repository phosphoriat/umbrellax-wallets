package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wallets/internal/config"
	"wallets/internal/database/pgsql"
	"wallets/internal/database/redis"
	"wallets/internal/database/vault"
	"wallets/internal/routes"
	"wallets/internal/services/api/bybit"
	_ "wallets/internal/services/chains"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	// Load config
	cfg := config.Load()
	if cfg == nil {
		log.Fatal("Failed to load config.")
	}

	// Init PostgreSQL and apply auto-migrations once, if needed
	if err := pgsql.Init(cfg); err != nil {
		log.Fatal("Failed to initialize PostgreSQL:", err)
	}
	defer pgsql.Close()

	// Init Vault
	if err := vault.Init(cfg); err != nil {
		log.Fatal("Failed to initialize Vault:", err)
	}
	defer vault.Close()

	// Init Redis
	if err := redis.Init(cfg); err != nil {
		log.Fatal("Failed to initialize Redis:", err)
	}
	defer redis.Close()

	/* Init services */
	// Init Bybit service
	bybit.Init(cfg)

	// Create app instance
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{
				"error": err.Error(),
			})
		},
	})

	/* Middleware */
	// Logger middleware
	errorLogFile, err := os.OpenFile("../logs/errors.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("error opening error log file: %v", err)
	}
	defer errorLogFile.Close()

	app.Use(logger.New(logger.Config{
		Format:     "[${time}] ERROR ${status} - ${method} ${path} - Error: ${error}\n",
		TimeFormat: "02.01.2006 15:04:05",
		Output:     errorLogFile,
		Next: func(c *fiber.Ctx) bool {
			return c.Response().StatusCode() < 400
		},
	}))

	// Recover middleware
	app.Use(recover.New())

	// Define API routes
	routes.SetupRoutes(app)

	// Server address
	address := fmt.Sprintf("%s:%s", cfg.AppUrl, cfg.AppPort)
	// Start HTTP server in goroutine for graceful shutdown
	go func() {
		log.Printf("Server starting on %s", address)
		if err := app.Listen(address); err != nil {
			log.Fatalf("server failed to start: %v", err)
		}
	}()

	// Server graceful shutdown
	gracefulShutdown(app)
}

// gracefulShutdown Graceful server shutdown
func gracefulShutdown(app *fiber.App) {
	// Create channel to signify a signal being sent
	c := make(chan os.Signal, 1)

	// When an interrupt or termination signal is sent, notify the channel
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// This blocks the main thread until an interrupt is received
	<-c
	log.Println("Gracefully shutting down...")

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := app.ShutdownWithContext(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exited")
}
