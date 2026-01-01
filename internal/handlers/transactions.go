package handlers

import (
	"wallets/internal/middleware"
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

type (
	// TransactionsHandler Instance of handler
	TransactionsHandler struct {
		service *services.TransactionsService
	}

	// GetTransactionCommissionQuery validation rules
	GetTransactionCommissionQuery struct {
		Network  string  `validate:"required,network"`
		Currency string  `validate:"required,currency"`
		To       string  `validate:"required,address"`
		Amount   float64 `validate:"required,gt=0"`
		Memo     string
	}
	// GetTransactionStatusQuery validation rules
	GetTransactionStatusQuery struct {
		Network string `validate:"required,network"`
		Hash    string `validate:"required"`
		From    string `validate:"omitempty,address"`
	}
	// SendTransactionQuery validation rules
	SendTransactionQuery struct {
		UserId   uint    `validate:"required,gte=0"`
		Network  string  `validate:"required,network"`
		Currency string  `validate:"required,currency"`
		To       string  `validate:"required,address"`
		Amount   float64 `validate:"required,gt=0"`
		Memo     string
	}
)

// NewTransactionsHandler Create new instance of TransactionsHandler
func NewTransactionsHandler(service *services.TransactionsService) *TransactionsHandler {
	return &TransactionsHandler{service}
}

// GetTransactionCommission
// GET /api/transactions/commission?network={network}&currency={currency}&to={to}&amount={amount}&memo={memo}
func (h *TransactionsHandler) GetTransactionCommission(c *fiber.Ctx) error {
	params := &GetTransactionCommissionQuery{
		Network:  c.Query("network"),
		Currency: c.Query("currency"),
		To:       c.Query("to"),
		Amount:   c.QueryFloat("amount"),
		Memo:     c.Query("memo", ""),
	}
	if err := middleware.ValidateStruct(params); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	res, err := h.service.GetTransactionCommission(params.Network, params.Currency, params.To, params.Amount, params.Memo)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}

// GetTransactionStatus
// GET /api/transactions/status?network={network}&hash={hash}&from={from}
func (h *TransactionsHandler) GetTransactionStatus(c *fiber.Ctx) error {
	params := &GetTransactionStatusQuery{
		Network: c.Query("network"),
		Hash:    c.Query("hash"),
		From:    c.Query("from", ""),
	}
	if err := middleware.ValidateStruct(params); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	res, err := h.service.GetTransactionStatus(params.Network, params.Hash, params.From)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": res})
}

// SendWithdrawal
// POST /api/transactions/withdrawal?user_id={userId}network={network}&currency={currency}&to={to}&amount={amount}&memo={memo}
func (h *TransactionsHandler) SendWithdrawal(c *fiber.Ctx) error {
	params := &SendTransactionQuery{
		UserId:   uint(c.QueryInt("user_id")),
		Network:  c.Query("network"),
		Currency: c.Query("currency"),
		To:       c.Query("to"),
		Amount:   c.QueryFloat("amount"),
		Memo:     c.Query("memo", ""),
	}
	if err := middleware.ValidateStruct(params); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	res, err := h.service.SendWithdrawal(params.UserId, params.Network, params.Currency, params.To, params.Amount, params.Memo)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"transaction": res})
}
