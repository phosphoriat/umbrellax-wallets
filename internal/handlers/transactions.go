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

	// GetTransactionCommissionQuery validation params
	GetTransactionCommissionQuery struct {
		Network  string  `validate:"required,oneof=trc20 erc20 bep20 ton bitcoin solana"`
		Currency string  `validate:"required,oneof=UMBRELLA USDT ETH TON BTC SOL"`
		To       string  `validate:"required"`
		Amount   float64 `validate:"required,gt=0"`
	}
	// GetTransactionStatusQuery validation params
	GetTransactionStatusQuery struct {
		Hash string `validate:"required"`
	}
	// SendTransactionQuery validation params
	SendTransactionQuery struct {
		Network  string  `validate:"required,oneof=trc20 erc20 bep20 ton bitcoin solana"`
		Currency string  `validate:"required,oneof=UMBRELLA USDT ETH TON BTC SOL"`
		To       string  `validate:"required"`
		Amount   float64 `validate:"required,gt=0"`
		Comment  string  `validate:""`
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
		return err
	}

	res := h.service.GetTransactionCommission(params.Currency, params.Network, params.To, params.Amount)
	return c.JSON(fiber.Map{"commission": res})
}

// GetTransactionStatus
// GET /api/transactions/status?hash={hash}
func (h *TransactionsHandler) GetTransactionStatus(c *fiber.Ctx) error {
	params := &GetTransactionStatusQuery{
		Hash: c.Query("hash"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return err
	}

	res := h.service.GetTransactionStatus(params.Hash)
	return c.JSON(fiber.Map{"status": res})
}

// SendTransaction
// POST /api/transactions?network={network}&currency={currency}&to={to}&amount={amount}&comment={comment}
func (h *TransactionsHandler) SendTransaction(c *fiber.Ctx) error {
	params := &SendTransactionQuery{
		Network:  c.Query("network"),
		Currency: c.Query("currency"),
		To:       c.Query("to"),
		Amount:   c.QueryFloat("amount"),
		Comment:  c.Query("comment"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return err
	}

	res := h.service.SendTransaction(params.Currency, params.Network, params.To, params.Amount, &params.Comment)
	return c.JSON(fiber.Map{"transaction": res})
}
