package handlers

import (
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App) {
	api := app.Group("/api")

	// Init route handlers
	tokensHandler := NewTokensHandler(services.NewTokensService())
	walletsHandler := NewWalletsHandler(services.NewWalletsService())
	transactionsHandler := NewTransactionsHandler(services.NewTransactionsService())

	// Health check
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	// Token route group
	tokens := api.Group("/tokens")
	tokens.Get("/", tokensHandler.GetTokenInfo)

	// Wallets route group
	wallets := api.Group("/wallets")
	wallets.Get("/", walletsHandler.GetUserWallet)
	wallets.Get("/master", walletsHandler.GetMasterWallet)

	// Transactions route group
	transactions := api.Group("/transactions")
	transactions.Get("/commission", transactionsHandler.GetTransactionCommission)
	transactions.Get("/status", transactionsHandler.GetTransactionStatus)
	transactions.Post("/", transactionsHandler.SendTransaction)
}
