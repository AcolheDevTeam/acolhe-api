package patient

import (
	"encoding/json"
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
	g.GET("/:id", h.get)
	g.POST("", h.create)
	g.POST("/:id/invitation", h.resend)
	g.POST("/:id/export", h.requestExport)
	e.GET("/invites/:token", h.validate)
	e.POST("/invites/:token/accept", h.accept)
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
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "falha ao solicitar exportação")
		}
	}
	return c.NoContent(http.StatusAccepted)
}

func (h *Handler) list(c echo.Context) error {
	patients, err := h.svc.List(c.Request().Context())
	if err != nil {
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
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return c.JSON(http.StatusOK, p)
}

func (h *Handler) create(c echo.Context) error {
	var req struct {
		FullName string `json:"fullName"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil || req.FullName == "" || req.Email == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "nome e e-mail são obrigatórios")
	}
	result, err := h.svc.Create(c.Request().Context(), req.FullName, req.Email)
	if err != nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "não foi possível criar o convite")
	}
	return c.JSON(http.StatusCreated, result)
}

func (h *Handler) resend(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	result, err := h.svc.Resend(c.Request().Context(), id)
	if errors.Is(err, ErrInviteRateLimited) {
		return echo.NewHTTPError(http.StatusTooManyRequests, err.Error())
	}
	if errors.Is(err, ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "não foi possível reenviar o convite")
	}
	return c.JSON(http.StatusOK, result)
}

func (h *Handler) validate(c echo.Context) error {
	status, err := h.svc.Validate(c.Request().Context(), c.Param("token"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "convite inválido ou expirado")
	}
	return c.JSON(http.StatusOK, status)
}

func (h *Handler) accept(c echo.Context) error {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil || len(req.Password) < 8 {
		return echo.NewHTTPError(http.StatusBadRequest, "senha inválida")
	}
	if err := h.svc.Accept(c.Request().Context(), c.Param("token"), req.Password); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "convite inválido, expirado ou já utilizado")
	}
	return c.NoContent(http.StatusNoContent)
}
