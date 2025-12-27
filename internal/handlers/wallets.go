package handlers

import (
	"wallets/internal/middleware"
	"wallets/internal/services"

	"github.com/gofiber/fiber/v2"
)

type (
	// WalletsHandler Instance of handler
	WalletsHandler struct {
		service *services.WalletsService
	}

	// GetUserWalletQuery validation rules
	GetUserWalletQuery struct {
		Network string `validate:"required,network"`
		UserId  uint   `validate:"required,gt=0"`
	}
	// GetMasterWalletQuery validation rules
	GetMasterWalletQuery struct {
		Network string `validate:"required,network"`
	}
)

// NewWalletsHandler Create new instance of TokensHandler
func NewWalletsHandler(service *services.WalletsService) *WalletsHandler {
	return &WalletsHandler{service}
}

// GetUserWallet
// GET /api/wallets?user_id={userId}&network={network}
func (h *WalletsHandler) GetUserWallet(c *fiber.Ctx) error {
	params := &GetUserWalletQuery{
		UserId:  uint(c.QueryInt("user_id")),
		Network: c.Query("network"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return c.JSON(err)
	}

	res, err := h.service.GetUserWallet(params.UserId, params.Network)
	if err != nil {
		return c.JSON(err)
	}
	return c.JSON(fiber.Map{"address": res})
}

// GetMasterWallet
// GET /api/wallets/master?network={network}
func (h *WalletsHandler) GetMasterWallet(c *fiber.Ctx) error {
	params := &GetMasterWalletQuery{
		Network: c.Query("network"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return c.JSON(err)
	}

	res, err := h.service.GetMasterWallet(params.Network)
	if err != nil {
		return c.JSON(err)
	}
	return c.JSON(res)
}
