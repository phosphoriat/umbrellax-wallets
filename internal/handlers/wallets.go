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

	// GetUserWalletQuery validation params
	GetUserWalletQuery struct {
		Network string `validate:"required,oneof=trc20 erc20 bep20 ton bitcoin solana"`
		UserId  int    `validate:"required,integer"`
	}
	// GetMasterWalletQuery validation params
	GetMasterWalletQuery struct {
		Network string `validate:"required,oneof=trc20 erc20 bep20 ton bitcoin solana"`
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
		UserId:  c.QueryInt("user_id"),
		Network: c.Query("network"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return err
	}

	res := h.service.GetUserWallet(params.UserId, params.Network)
	return c.JSON(fiber.Map{"address": res})
}

// GetMasterWallet
// GET /api/wallets/master?network={network}
func (h *WalletsHandler) GetMasterWallet(c *fiber.Ctx) error {
	params := &GetMasterWalletQuery{
		Network: c.Query("network"),
	}

	if err := middleware.ValidateStruct(params); err != nil {
		return err
	}

	res := h.service.GetMasterWallet(params.Network)
	return c.JSON(fiber.Map{"wallet": res})
}
