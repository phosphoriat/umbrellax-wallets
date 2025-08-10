package routes

import (
	"wallets/internal/handlers"
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App) {
	// Set api routes group
	api := app.Group("/api")

	// Init route handlers
	tokensHandler := handlers.NewTokensHandler(services.NewTokensService())
	walletsHandler := handlers.NewWalletsHandler(services.NewWalletsService())
	transactionsHandler := handlers.NewTransactionsHandler(services.NewTransactionsService())

	// Health check
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Tokens routes group
	tokens := api.Group("/tokens")
	tokens.Get("/", tokensHandler.GetTokenInfo)

	// Wallets routes group
	wallets := api.Group("/wallets")
	wallets.Get("/", walletsHandler.GetUserWallet)
	wallets.Get("/master", walletsHandler.GetMasterWallet)

	// Transactions routes group
	transactions := api.Group("/transactions")
	transactions.Get("/commission", transactionsHandler.GetTransactionCommission)
	transactions.Get("/status", transactionsHandler.GetTransactionStatus)
	transactions.Post("/", transactionsHandler.SendTransaction)
}
