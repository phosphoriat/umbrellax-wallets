package handlers

import (
	"wallets/internal/middleware"
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

type (
	// TokensHandler Instance of handler
	TokensHandler struct {
		service *services.TokensService
	}

	// GetTokenInfoQuery validation params
	GetTokenInfoQuery struct {
		Ticker string `validate:"required,oneof=UMBRELLA USDT ETH TON BTC SOL"`
	}
)

// NewTokensHandler Create new instance of TokensHandler
func NewTokensHandler(service *services.TokensService) *TokensHandler {
	return &TokensHandler{service}
}

// GetTokenInfo
// GET /api/tokens?ticker={ticker}
func (h *TokensHandler) GetTokenInfo(c *fiber.Ctx) error {
	params := &GetTokenInfoQuery{
		Ticker: c.Query("ticker"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return err
	}

	res := h.service.GetTokenInfo(params.Ticker)
	return c.JSON(fiber.Map{"token": res})
}
