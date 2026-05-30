package document

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
	g := e.Group("/documents")
	g.GET("", h.list) // ?patientId=...
	g.POST("/generate", h.generate)
}

func (h *Handler) generate(c echo.Context) error {
	var req GenerateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	doc, err := h.svc.GeneratePDF(c.Request().Context(), req)
	if err != nil {
		if err == ErrPsychologistRequired {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "falha ao gerar documento")
	}
	return c.JSON(http.StatusAccepted, doc)
}

func (h *Handler) list(c echo.Context) error {
	patientID, err := uuid.Parse(c.QueryParam("patientId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "patientId inválido")
	}
	docs, err := h.svc.List(c.Request().Context(), patientID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar documentos")
	}
	return c.JSON(http.StatusOK, docs)
}
