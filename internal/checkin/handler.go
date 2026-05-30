package checkin

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(e *echo.Echo) {
	g := e.Group("/checkins")
	g.POST("", h.create)
	g.GET("", h.list) // ?patientId=...
}

func (h *Handler) create(c echo.Context) error {
	var req CreateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	ch, err := h.svc.Create(c.Request().Context(), req)
	if err != nil {
		switch err {
		case ErrInvalidMood:
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		case ErrPatientNotInOrg:
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}
	return c.JSON(http.StatusCreated, ch)
}

func (h *Handler) list(c echo.Context) error {
	patientID, err := uuid.Parse(c.QueryParam("patientId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "patientId inválido")
	}
	items, err := h.svc.List(c.Request().Context(), patientID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar check-ins")
	}
	return c.JSON(http.StatusOK, items)
}
