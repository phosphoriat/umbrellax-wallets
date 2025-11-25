package routes

import (
	"wallets/internal/handlers"
	"wallets/internal/repository"
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App) {
	// Init repositories
	walletRepo := repository.NewWalletRepository()

	// Init routes handlers
	tokensHandler := handlers.NewTokensHandler(services.NewTokensService())
	walletsHandler := handlers.NewWalletsHandler(services.NewWalletsService(walletRepo))
	transactionsHandler := handlers.NewTransactionsHandler(services.NewTransactionsService())

	// Set api routes group
	api := app.Group("/api")

	// Health check route
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Tokens routes group
	tokens := api.Group("/tokens")
	tokens.Get("/:ticker", tokensHandler.GetTokenInfo)

	// Wallets routes group
	wallets := api.Group("/wallets")
	wallets.Get("/", walletsHandler.GetUserWallet)
	wallets.Get("/master", walletsHandler.GetMasterWallet)

	// Transactions routes group
	transactions := api.Group("/transactions")
	transactions.Get("/commission", transactionsHandler.GetTransactionCommission)
	transactions.Get("/status", transactionsHandler.GetTransactionStatus)
	transactions.Post("/withdrawal", transactionsHandler.SendWithdrawal)
}
