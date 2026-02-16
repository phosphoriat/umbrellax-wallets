package handlers

import (
	"wallets/internal/services"

	"github.com/gofiber/fiber/v3"
)

type (
	// ActionsHandler Instance of handler
	ActionsHandler struct {
		service *services.ActionsService
	}

	// GetActionsQuery query params
	GetActionsQuery struct {
		UserId uint
		Type   string
		Status string
	}
	// RunAutoTransfersQuery query params
	RunAutoTransfersQuery struct {
		UserId uint
	}
)

// NewActionsHandler Create new instance of ActionsHandler
func NewActionsHandler(service *services.ActionsService) *ActionsHandler {
	return &ActionsHandler{service}
}

// GetActions
// GET /api/actions?user_id={userId}&type={type}&status={status}
func (h *ActionsHandler) GetActions(c *fiber.Ctx) error {
	params := &GetActionsQuery{
		UserId: uint(c.QueryInt("user_id")),
		Type:   c.Query("type"),
		Status: c.Query("status"),
	}

	res, err := h.service.GetActions(params.UserId, params.Type, params.Status)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}

// RunAutoTransfers
// POST /api/actions/auto-transfer?user_id={userId}
func (h *ActionsHandler) RunAutoTransfers(c *fiber.Ctx) error {
	params := &RunAutoTransfersQuery{
		UserId: uint(c.QueryInt("user_id")),
	}

	res, err := h.service.RunAutoTransfers(params.UserId)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}
