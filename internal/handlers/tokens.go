package handlers

import (
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

type TokensHandler struct {
	tokensService *services.TokensService
}

func NewTokensHandler(tokensService *services.TokensService) *TokensHandler {
	return &TokensHandler{tokensService: tokensService}
}

func (h *TokensHandler) GetTokenInfo(c *fiber.Ctx) error {
	res := h.tokensService.GetTokenInfo()
	return c.JSON(fiber.Map{"address": res})
}
