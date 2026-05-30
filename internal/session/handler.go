package session

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
	g := e.Group("/sessions")
	g.POST("", h.create)
	g.GET("", h.list)                         // ?patientId=...
	g.GET("/timeline/:patientId", h.timeline) // timeline unificada do paciente
}

func (h *Handler) create(c echo.Context) error {
	var req CreateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s, err := h.svc.Create(c.Request().Context(), req)
	if err != nil {
		switch err {
		case ErrNoActiveRelationship:
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		case ErrPsychologistRequired:
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}
	return c.JSON(http.StatusCreated, s)
}

func (h *Handler) list(c echo.Context) error {
	patientID, err := uuid.Parse(c.QueryParam("patientId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "patientId inválido")
	}
	sessions, err := h.svc.List(c.Request().Context(), patientID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar sessões")
	}
	return c.JSON(http.StatusOK, sessions)
}

func (h *Handler) timeline(c echo.Context) error {
	patientID, err := uuid.Parse(c.Param("patientId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "patientId inválido")
	}
	items, err := h.svc.Timeline(c.Request().Context(), patientID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao montar timeline")
	}
	return c.JSON(http.StatusOK, items)
}
