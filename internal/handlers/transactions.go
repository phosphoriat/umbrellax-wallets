package handlers

import (
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

type TransactionsHandler struct {
	transactionsService *services.TransactionsService
}

func NewTransactionsHandler(transactionsService *services.TransactionsService) *TransactionsHandler {
	return &TransactionsHandler{transactionsService: transactionsService}
}

// GetTransactionCommission
// GET /api/transactions/commission
func (h *TransactionsHandler) GetTransactionCommission(c *fiber.Ctx) error {
	res := h.transactionsService.GetTransactionCommission()
	return c.JSON(fiber.Map{"commission": res})
}

// GetTransactionStatus
// GET /api/transactions/status
func (h *TransactionsHandler) GetTransactionStatus(c *fiber.Ctx) error {
	res := h.transactionsService.GetTransactionStatus()
	return c.JSON(fiber.Map{"status": res})
}

// SendTransaction
// POST /api/transactions
func (h *TransactionsHandler) SendTransaction(c *fiber.Ctx) error {
	res := h.transactionsService.SendTransaction()
	return c.JSON(fiber.Map{"result": res})
}
