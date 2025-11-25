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
	"wallets/internal/database/clickhouse"
	"wallets/internal/database/redis"
	"wallets/internal/database/vault"
	"wallets/internal/routes"
	"wallets/internal/scheduler"
	"wallets/internal/services/api/bybit"
	_ "wallets/internal/services/chains"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	// Load config
	cfg, err := config.Load()
	if err == nil {
		log.Fatal("Failed to load config from env")
	}

	/* Init services */
	// Init ClickHouse
	if err := clickhouse.Init(cfg); err != nil {
		log.Fatal("Failed to initialize ClickHouse:", err)
	}
	defer clickhouse.Close()

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

	// Init Bybit service
	bybit.Init(cfg)

	// Init scheduler
	scheduler.Init()

	/* Create app instance */
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

	/* Define routes */
	routes.SetupRoutes(app)

	/* Start HTTP server */
	// Get server address and port from config
	address := fmt.Sprintf("%s:%s", cfg.AppUrl, cfg.AppPort)
	// Start HTTP server in goroutine for graceful shutdown
	go func() {
		log.Printf("Server starting on %s", address)
		if err := app.Listen(address); err != nil {
			log.Fatalf("server failed to start: %v", err)
		}
	}()

	// Set server graceful shutdown
	ServerGracefulShutdown(app)
}

// ServerGracefulShutdown Set server graceful shutdown
func ServerGracefulShutdown(app *fiber.App) {
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
