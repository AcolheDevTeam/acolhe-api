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
	g.GET("", h.list)
	g.POST("", h.assign)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.review)
	g.GET("/assignments", h.listAssignments) // ?patientId=...
	g.POST("/assignments/:id/responses", h.submitResponse)
	g.GET("/assignments/:id/responses", h.listResponses)
}

func (h *Handler) assign(c echo.Context) error {
	var req AssignRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}
	a, err := h.svc.Assign(c.Request().Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrTemplateNotFound), errors.Is(err, ErrPatientNotFound):
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		case errors.Is(err, ErrPsychologistRequired):
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "falha ao atribuir atividade")
		}
	}
	return c.JSON(http.StatusCreated, a)
}

func (h *Handler) list(c echo.Context) error {
	if c.QueryParam("patientId") == "" {
		items, err := h.svc.ListAll(c.Request().Context())
		if err != nil {
			if errors.Is(err, ErrPsychologistRequired) {
				return echo.NewHTTPError(http.StatusForbidden, err.Error())
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar atividades")
		}
		return c.JSON(http.StatusOK, items)
	}
	patientID, err := uuid.Parse(c.QueryParam("patientId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "patientId inválido")
	}
	items, err := h.svc.ListAssignments(c.Request().Context(), patientID)
	if err != nil {
		if errors.Is(err, ErrPsychologistRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar atividades")
	}
	return c.JSON(http.StatusOK, items)
}

func (h *Handler) get(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	a, err := h.svc.Get(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, ErrPsychologistRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		if errors.Is(err, ErrAssignmentNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao buscar atividade")
	}
	return c.JSON(http.StatusOK, a)
}

func (h *Handler) review(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&req); err != nil || req.Status != "reviewed" {
		return echo.NewHTTPError(http.StatusBadRequest, "status inválido")
	}
	a, err := h.svc.MarkReviewed(c.Request().Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrPsychologistRequired):
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		case errors.Is(err, ErrAssignmentNotFound):
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		case errors.Is(err, ErrInvalidStatus):
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "falha ao revisar atividade")
		}
	}
	return c.JSON(http.StatusOK, a)
}

func (h *Handler) submitResponse(c echo.Context) error {
	assignmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	resp, err := h.svc.Submit(c.Request().Context(), assignmentID)
	if err != nil {
		if errors.Is(err, ErrPatientRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		if errors.Is(err, ErrSubmissionNotAllowed) {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
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
		if errors.Is(err, ErrPsychologistRequired) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "erro ao listar atribuições")
	}
	return c.JSON(http.StatusOK, assignments)
}
