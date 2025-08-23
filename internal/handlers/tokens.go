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

	// GetTokenInfoQuery validation rules
	GetTokenInfoQuery struct {
		Network  string `validate:"required,network"`
		Currency string `validate:"required,currency"`
	}
)

// NewTokensHandler Create new instance of TokensHandler
func NewTokensHandler(service *services.TokensService) *TokensHandler {
	return &TokensHandler{service}
}

// GetTokenInfo
// GET /api/tokens/:ticker
func (h *TokensHandler) GetTokenInfo(c *fiber.Ctx) error {
	params := &GetTokenInfoQuery{
		Network:  c.Query("network"),
		Currency: c.Query("currency"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return c.JSON(err)
	}

	res, err := h.service.GetTokenInfo(params.Network, params.Currency)
	if err != nil {
		return c.JSON(err)
	}
	return c.JSON(fiber.Map{"info": res})
}
