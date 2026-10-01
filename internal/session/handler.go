package session

import (
	"errors"
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
	g.PUT("/:id/notes", h.saveNotes)
	e.POST("/appointments/:id/session", h.fromAppointment)
	g.GET("", h.list)                         // ?patientId=... (opcional)
	g.GET("/timeline/:patientId", h.timeline) // timeline unificada do paciente
	g.GET("/:id", h.get)
}

func (h *Handler) create(c echo.Context) error {
	var req CreateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s, err := h.svc.Create(c.Request().Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidInput):
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrNoActiveRelationship):
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		case errors.Is(err, ErrPsychologistRequired):
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}
	return c.JSON(http.StatusCreated, s)
}

func (h *Handler) list(c echo.Context) error {
	if c.QueryParam("patientId") == "" {
		sessions, err := h.svc.ListAll(c.Request().Context())
		if err != nil {
			if errors.Is(err, ErrPsychologistRequired) {
				return echo.NewHTTPError(http.StatusForbidden, err.Error())
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar sessões")
		}
		return c.JSON(http.StatusOK, sessions)
	}
	patientID, err := uuid.Parse(c.QueryParam("patientId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "patientId inválido")
	}
	sessions, err := h.svc.List(c.Request().Context(), patientID)
	if err != nil {
		if errors.Is(err, ErrPsychologistRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar sessões")
	}
	return c.JSON(http.StatusOK, sessions)
}

func (h *Handler) get(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	s, err := h.svc.Get(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, ErrPsychologistRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		if errors.Is(err, ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao buscar sessão")
	}
	return c.JSON(http.StatusOK, s)
}

func (h *Handler) timeline(c echo.Context) error {
	patientID, err := uuid.Parse(c.Param("patientId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "patientId inválido")
	}
	items, err := h.svc.Timeline(c.Request().Context(), patientID)
	if err != nil {
		if errors.Is(err, ErrPsychologistRequired) || errors.Is(err, ErrNoActiveRelationship) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao montar timeline")
	}
	return c.JSON(http.StatusOK, items)
}

func (h *Handler) fromAppointment(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	result, err := h.svc.FromAppointment(c.Request().Context(), id)
	if err != nil {
		return sessionWriteError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *Handler) saveNotes(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	var req struct {
		Notes   string `json:"notes"`
		Version int32  `json:"version"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "dados da evolução inválidos")
	}
	result, err := h.svc.SaveNotes(c.Request().Context(), id, req.Notes, req.Version)
	if err != nil {
		return sessionWriteError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func sessionWriteError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrPsychologistRequired), errors.Is(err, ErrNoActiveRelationship), errors.Is(err, ErrRecordLocked):
		return echo.NewHTTPError(http.StatusForbidden, err.Error())
	case errors.Is(err, ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, ErrAppointmentNotReady), errors.Is(err, ErrVersionConflict):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "não foi possível salvar o prontuário")
	}
}
