package onboarding

import (
	"errors"
	"net/http"
	"net/netip"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(e *echo.Echo) {
	group := e.Group("/onboarding/invitations")
	group.GET("/:token", h.get)
	group.POST("/:token/accept", h.accept)
	group.POST("/:token/decline", h.decline)
}

func (h *Handler) get(c echo.Context) error {
	invitation, err := h.svc.Get(c.Request().Context(), c.Param("token"))
	if err != nil {
		return onboardingHTTPError(err)
	}
	return c.JSON(http.StatusOK, invitation)
}

func (h *Handler) accept(c echo.Context) error {
	var req AcceptRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}
	result, err := h.svc.Accept(
		c.Request().Context(), c.Param("token"), req,
		clientIP(c), c.Request().UserAgent(),
	)
	if err != nil {
		return onboardingHTTPError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *Handler) decline(c echo.Context) error {
	if err := h.svc.Decline(
		c.Request().Context(), c.Param("token"),
		clientIP(c), c.Request().UserAgent(),
	); err != nil {
		return onboardingHTTPError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func onboardingHTTPError(err error) error {
	switch {
	case errors.Is(err, ErrInvitationNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvitationGone):
		return echo.NewHTTPError(http.StatusGone, err.Error())
	case errors.Is(err, ErrInvalidInput):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrRequiredConsent), errors.Is(err, ErrEmailInUse):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "falha no onboarding")
	}
}

func clientIP(c echo.Context) netip.Addr {
	ip, err := netip.ParseAddr(c.RealIP())
	if err != nil {
		return netip.IPv4Unspecified()
	}
	return ip.Unmap()
}
