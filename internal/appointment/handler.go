package appointment

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
	g := e.Group("/appointments")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PUT("/:id", h.reschedule)
	g.PUT("/:id/status", h.transitionStatus)
}

func (h *Handler) create(c echo.Context) error {
	var req CreateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	a, err := h.svc.Create(c.Request().Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidInput):
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrScheduleConflict):
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		case errors.Is(err, ErrPsychologistRequired):
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		case errors.Is(err, ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}
	return c.JSON(http.StatusCreated, a)
}

func (h *Handler) transitionStatus(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	var request struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&request); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}
	appointment, err := h.svc.TransitionStatus(c.Request().Context(), id, request.Status)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidInput):
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrPsychologistRequired):
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		case errors.Is(err, ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		case errors.Is(err, ErrInvalidStatusTransition):
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "falha ao atualizar agendamento")
		}
	}
	return c.JSON(http.StatusOK, appointment)
}

func (h *Handler) list(c echo.Context) error {
	appointments, err := h.svc.List(c.Request().Context())
	if err != nil {
		if errors.Is(err, ErrPsychologistRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar agenda")
	}
	return c.JSON(http.StatusOK, appointments)
}

func (h *Handler) get(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	result, err := h.svc.Get(c.Request().Context(), id)
	if err != nil {
		return appointmentWriteError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *Handler) reschedule(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	var req CreateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "dados do agendamento inválidos")
	}
	result, err := h.svc.Reschedule(c.Request().Context(), id, req)
	if err != nil {
		return appointmentWriteError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func appointmentWriteError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrPsychologistRequired):
		return echo.NewHTTPError(http.StatusForbidden, err.Error())
	case errors.Is(err, ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, ErrScheduleConflict), errors.Is(err, ErrInvalidStatusTransition):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "não foi possível salvar o agendamento")
	}
}
