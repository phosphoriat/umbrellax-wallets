package handlers

import (
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

type WalletsHandler struct {
	walletsService *services.WalletsService
}

func NewWalletsHandler(walletsService *services.WalletsService) *WalletsHandler {
	return &WalletsHandler{walletsService: walletsService}
}

func (h *WalletsHandler) GetUserWallet(c *fiber.Ctx) error {
	res := h.walletsService.GetUserWallet()
	return c.JSON(fiber.Map{"address": res})
}

func (h *WalletsHandler) GetMasterWallet(c *fiber.Ctx) error {
	res := h.walletsService.GetMasterWallet()
	return c.JSON(fiber.Map{"wallet": res})
}
