package notification

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(e *echo.Echo) {
	g := e.Group("/notifications")
	g.POST("/reminders", h.reminder)
}

func (h *Handler) reminder(c echo.Context) error {
	var req ReminderRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := h.svc.EnqueueReminder(c.Request().Context(), req); err != nil {
		switch {
		case errors.Is(err, ErrInvalidInput):
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrQueueUnavailable):
			return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "falha ao enfileirar lembrete")
		}
	}
	return c.NoContent(http.StatusAccepted)
}
