package activity

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
	g := e.Group("/activities")
	g.GET("/templates", h.listTemplates)
	g.GET("/assignments", h.listAssignments) // ?patientId=...
	g.POST("/assignments/:id/responses", h.submitResponse)
	g.GET("/assignments/:id/responses", h.listResponses)
}

func (h *Handler) submitResponse(c echo.Context) error {
	assignmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	resp, err := h.svc.Submit(c.Request().Context(), assignmentID)
	if err != nil {
		if errors.Is(err, ErrAssignmentNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "falha ao submeter resposta")
	}
	return c.JSON(http.StatusCreated, resp)
}

func (h *Handler) listResponses(c echo.Context) error {
	assignmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	resps, err := h.svc.ListResponses(c.Request().Context(), assignmentID)
	if err != nil {
		if errors.Is(err, ErrAssignmentNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar respostas")
	}
	return c.JSON(http.StatusOK, resps)
}

func (h *Handler) listTemplates(c echo.Context) error {
	templates, err := h.svc.ListTemplates(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar templates")
	}
	return c.JSON(http.StatusOK, templates)
}

func (h *Handler) listAssignments(c echo.Context) error {
	patientID, err := uuid.Parse(c.QueryParam("patientId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "patientId inválido")
	}
	assignments, err := h.svc.ListAssignments(c.Request().Context(), patientID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar atribuições")
	}
	return c.JSON(http.StatusOK, assignments)
}
