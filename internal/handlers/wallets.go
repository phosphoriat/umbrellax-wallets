package handlers

import (
	"wallets/internal/middleware"
	"wallets/internal/services"

	"github.com/gofiber/fiber/v3"
)

type (
	// WalletsHandler Instance of handler
	WalletsHandler struct {
		service *services.WalletsService
	}

	// GetUserWalletAddressQuery validation rules
	GetUserWalletAddressQuery struct {
		Network    string `validate:"required,network"`
		UserId     uint   `validate:"required,gt=0"`
		UserNumber string `validate:"required"`
	}
	// GetUserWalletInfoQuery validation rules
	GetUserWalletInfoQuery struct {
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

// GetUserWalletAddress
// GET /api/wallets?user_id={userId}&user_number={number}&network={network}
func (h *WalletsHandler) GetUserWalletAddress(c *fiber.Ctx) error {
	params := &GetUserWalletAddressQuery{
		UserId:     uint(c.QueryInt("user_id")),
		UserNumber: c.Query("user_number"),
		Network:    c.Query("network"),
	}
	if err := middleware.ValidateStruct(params); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	res, err := h.service.GetUserWalletAddress(params.UserId, params.UserNumber, params.Network)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"address": res})
}

// GetUserWalletInfo
// GET /api/wallets/user?user_id={userId}&network={network}
func (h *WalletsHandler) GetUserWalletInfo(c *fiber.Ctx) error {
	params := &GetUserWalletInfoQuery{
		UserId:  uint(c.QueryInt("user_id")),
		Network: c.Query("network"),
	}
	if err := middleware.ValidateStruct(params); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	res, err := h.service.GetUserWalletInfo(params.UserId, params.Network)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}

// GetMasterWalletInfo
// GET /api/wallets/master?network={network}
func (h *WalletsHandler) GetMasterWalletInfo(c *fiber.Ctx) error {
	params := &GetMasterWalletQuery{
		Network: c.Query("network"),
	}
	if err := middleware.ValidateStruct(params); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	res, err := h.service.GetMasterWalletInfo(params.Network)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}
