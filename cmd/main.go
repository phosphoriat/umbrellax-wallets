package main

import (
	"log"

	"github.com/gofiber/fiber/v2"

	"wallets/internal/config"
	"wallets/internal/handlers"
)

func main() {
	// Load config
	cfg := config.Load()

	// Create app instance
	app := fiber.New(fiber.Config{})

	// Define routes
	handlers.SetupRoutes(app)

	// Start HTTP server
	log.Fatal(app.Listen(":" + cfg.Port))
}
