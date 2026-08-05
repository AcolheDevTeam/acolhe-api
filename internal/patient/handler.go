package patient

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
	g := e.Group("/patients")
	g.GET("", h.list)
	g.POST("", h.create)
	g.GET("/:id", h.get)
	g.POST("/:id/export", h.requestExport)
}

func (h *Handler) create(c echo.Context) error {
	var req CreateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}
	if key := c.Request().Header.Get("Idempotency-Key"); key != "" {
		parsed, err := uuid.Parse(key)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Idempotency-Key inválida")
		}
		req.IdempotencyKey = parsed
	} else {
		req.IdempotencyKey = uuid.New()
	}
	p, err := h.svc.Create(c.Request().Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidInput):
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrPsychologistRequired):
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "falha ao criar paciente")
		}
	}
	return c.JSON(http.StatusCreated, p)
}

func (h *Handler) requestExport(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	if err := h.svc.RequestExport(c.Request().Context(), id); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		case errors.Is(err, ErrQueueUnavailable):
			return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
		case errors.Is(err, ErrPsychologistRequired):
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "falha ao solicitar exportação")
		}
	}
	return c.NoContent(http.StatusAccepted)
}

func (h *Handler) list(c echo.Context) error {
	patients, err := h.svc.List(c.Request().Context())
	if err != nil {
		if errors.Is(err, ErrPsychologistRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar pacientes")
	}
	return c.JSON(http.StatusOK, patients)
}

func (h *Handler) get(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	p, err := h.svc.Get(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, ErrPsychologistRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return c.JSON(http.StatusOK, p)
}
