package main

import (
	"context"
	"fmt"
	"io"
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
	"wallets/internal/services/api/rates"
	_ "wallets/internal/services/chains"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load config from env:", err)
	}

	// Setup logs output
	appLogFile, err := os.OpenFile("../logs/wallets.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open app log file: %v", err)
	}
	defer appLogFile.Close()

	errLogFile, err := os.OpenFile("../logs/error.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open error log file: %v", err)
	}
	defer errLogFile.Close()

	var appLogOutput io.Writer
	if cfg.AppEnv == "production" {
		appLogOutput = appLogFile
	} else {
		appLogOutput = io.MultiWriter(os.Stdout, appLogFile)
	}
	log.SetOutput(appLogOutput)

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

	// Init scheduler
	schedulerInstance, err := scheduler.Init()
	if err != nil {
		log.Fatal("Failed to init scheduler:", err)
	}

	// Init rates updater
	rates.GetClient().Start(ctx)
	defer rates.GetClient().Stop()

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
	app.Use(logger.New(logger.Config{
		Format:     "[${time}] ERROR ${status} - ${method} ${path} - Error: ${error}\n",
		TimeFormat: "02.01.2006 15:04:05",
		Output:     errLogFile,
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
	// Start HTTP server in goroutine
	errChan := make(chan error, 1)
	go func() {
		log.Printf("Server starting on %s", address)
		errChan <- app.Listen(address)
	}()

	// Application graceful shutdown
	GracefulShutdown(app, errChan, schedulerInstance)
}

// GracefulShutdown Application graceful shutdown
func GracefulShutdown(app *fiber.App, errChan <-chan error, schedulerInstance *scheduler.Scheduler) {
	// Intercept termination signals
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Wait for signal or server error
	select {
	case <-signalCtx.Done():
		log.Println("Application received shutdown signal")
	case err := <-errChan:
		log.Printf("Server error: %v", err)
	}

	log.Println("Application is shutting down...")

	// Create deadline for shutdown (10s)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Graceful shutdown app and scheduler
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	if err := schedulerInstance.Shutdown(); err != nil {
		log.Printf("Scheduler shutdown error: %v", err)
	}

	log.Println("Application successfully shutdown")
}
