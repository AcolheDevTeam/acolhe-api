package account

import (
	"errors"
	"net"
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
	e.POST("/signup", h.signup)
	e.GET("/me", h.me)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type signupRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	FullName     string `json:"fullName"`
	CRPNumber    string `json:"crpNumber"`
	CRPState     string `json:"crpState"`
	CPF          string `json:"cpf"`
	Approach     string `json:"approach"`
	AcceptTerms  bool   `json:"acceptTerms"`
	TermsVersion string `json:"termsVersion"`
}

func (h *Handler) signup(c echo.Context) error {
	var req signupRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}
	res, err := h.svc.Signup(c.Request().Context(), SignupInput{
		Email: req.Email, Password: req.Password, FullName: req.FullName,
		CRPNumber: req.CRPNumber, CRPState: req.CRPState, CPF: req.CPF,
		Approach: req.Approach, AcceptTerms: req.AcceptTerms, TermsVersion: req.TermsVersion,
		IPAddress: net.ParseIP(c.RealIP()),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrSignupInvalid):
			return echo.NewHTTPError(http.StatusBadRequest, "dados de cadastro inválidos")
		case errors.Is(err, ErrSignupConflict):
			return echo.NewHTTPError(http.StatusConflict, "cadastro não pôde ser concluído")
		default:
			return echo.NewHTTPError(http.StatusServiceUnavailable, "cadastro indisponível")
		}
	}
	return c.JSON(http.StatusCreated, res)
}

func (h *Handler) login(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}

	res, err := h.svc.Login(c.Request().Context(), req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
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
