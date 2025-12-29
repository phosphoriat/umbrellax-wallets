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
	userRepo := repository.NewUserRepository()
	actionRepo := repository.NewActionRepository()

	// Init services
	tokensService := services.NewTokensService()
	walletsService := services.NewWalletsService(walletRepo, userRepo)
	transactionsService := services.NewTransactionsService()
	actionsService := services.NewActionsService(actionRepo, walletRepo)

	// Init handlers
	tokensHandler := handlers.NewTokensHandler(tokensService)
	walletsHandler := handlers.NewWalletsHandler(walletsService)
	transactionsHandler := handlers.NewTransactionsHandler(transactionsService)
	actionsHandler := handlers.NewActionsHandler(actionsService)

	// Set api routes group
	api := app.Group("/api")

	// Health check route
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Tokens routes group
	tokens := api.Group("/tokens")
	tokens.Get("/", tokensHandler.GetTokenInfo)

	// Wallets routes group
	wallets := api.Group("/wallets")
	wallets.Get("/", walletsHandler.GetUserWalletAddress)
	wallets.Get("/user", walletsHandler.GetUserWalletInfo)
	wallets.Get("/master", walletsHandler.GetMasterWalletInfo)

	// Transactions routes group
	transactions := api.Group("/transactions")
	transactions.Get("/commission", transactionsHandler.GetTransactionCommission)
	transactions.Get("/status", transactionsHandler.GetTransactionStatus)
	transactions.Post("/withdrawal", transactionsHandler.SendWithdrawal)

	// Actions routes group
	actions := api.Group("/actions")
	actions.Get("/", actionsHandler.GetActions)
	actions.Post("/auto-transfer", actionsHandler.RunAutoTransfers)
}
