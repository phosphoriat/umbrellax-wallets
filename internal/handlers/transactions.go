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
		Network  string  `validate:"required,oneof=tron ethereum bsc ton bitcoin solana"`
		Currency string  `validate:"required,oneof=UMBRELLA USDT ETH TON BTC SOL"`
		To       string  `validate:"required"`
		Amount   float64 `validate:"required,gt=0"`
	}
	// GetTransactionStatusQuery validation rules
	GetTransactionStatusQuery struct {
		Network string `validate:"required,oneof=tron ethereum bsc ton bitcoin solana"`
		Hash    string `validate:"required"`
	}
	// SendTransactionQuery validation rules
	SendTransactionQuery struct {
		Network  string  `validate:"required,oneof=tron ethereum bsc ton bitcoin solana"`
		Currency string  `validate:"required,oneof=UMBRELLA USDT ETH TON BTC SOL"`
		To       string  `validate:"required"`
		Amount   float64 `validate:"required,gt=0"`
	}
)

// NewTransactionsHandler Create new instance of TransactionsHandler
func NewTransactionsHandler(service *services.TransactionsService) *TransactionsHandler {
	return &TransactionsHandler{service}
}

// GetTransactionCommission
// GET /api/transactions/commission?network={network}&currency={currency}&to={to}&amount={amount}
func (h *TransactionsHandler) GetTransactionCommission(c *fiber.Ctx) error {
	params := &GetTransactionCommissionQuery{
		Network:  c.Query("network"),
		Currency: c.Query("currency"),
		To:       c.Query("to"),
		Amount:   c.QueryFloat("amount"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return c.JSON(err)
	}

	res, err := h.service.GetTransactionCommission(params.Currency, params.Network, params.To, params.Amount)
	if err != nil {
		return c.JSON(err)
	}
	return c.JSON(res)
}

// GetTransactionStatus
// GET /api/transactions/status?network={network}&hash={hash}
func (h *TransactionsHandler) GetTransactionStatus(c *fiber.Ctx) error {
	params := &GetTransactionStatusQuery{
		Network: c.Query("network"),
		Hash:    c.Query("hash"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return c.JSON(err)
	}

	res, err := h.service.GetTransactionStatus(params.Network, params.Hash)
	if err != nil {
		return c.JSON(err)
	}
	return c.JSON(fiber.Map{"status": res})
}

// SendWithdrawal
// POST /api/transactions/withdrawal?network={network}&currency={currency}&to={to}&amount={amount}
func (h *TransactionsHandler) SendWithdrawal(c *fiber.Ctx) error {
	params := &SendTransactionQuery{
		Network:  c.Query("network"),
		Currency: c.Query("currency"),
		To:       c.Query("to"),
		Amount:   c.QueryFloat("amount"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return c.JSON(err)
	}

	res, err := h.service.SendWithdrawal(params.Currency, params.Network, params.To, params.Amount)
	if err != nil {
		return c.JSON(err)
	}
	return c.JSON(fiber.Map{"transaction": res})
}
