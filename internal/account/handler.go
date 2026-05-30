package account

import (
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
	e.POST("/login", h.login)
	e.GET("/me", h.me)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) login(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}

	res, err := h.svc.Login(c.Request().Context(), req.Email, req.Password)
	if err != nil {
		switch err {
		case ErrInvalidCredentials:
			return echo.NewHTTPError(http.StatusUnauthorized, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "falha ao gerar sessão")
		}
	}
	return c.JSON(http.StatusOK, res)
}

func (h *Handler) me(c echo.Context) error {
	user, err := h.svc.Me(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return c.JSON(http.StatusOK, user)
}
