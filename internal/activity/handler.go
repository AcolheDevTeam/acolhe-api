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
	g.POST("/templates", h.createTemplate)
	g.GET("/templates/:id", h.getTemplate)
	g.PUT("/templates/:id", h.updateTemplate)
	g.POST("/templates/:id/archive", h.archiveTemplate)
	g.GET("", h.list)
	g.POST("", h.assign)
	g.GET("/:id", h.get)
	g.PUT("/:id/review", h.review)
	g.GET("/assignments", h.listAssignments) // ?patientId=...
	g.GET("/assignments/:id/responses", h.listResponses)

	// Portal da paciente. Fica no domínio activity porque é dado de atividade; o
	// grupo /patient é compartilhado com internal/patient, que registra o resto.
	//
	// A submissão precisa morar aqui, e não sob /activities: o guard de papel em
	// middleware/tenant.go bloqueia todo prefixo /activities para pacientes, então
	// POST /activities/assignments/:id/responses nunca foi alcançável por elas.
	// Usar o prefixo que já é da paciente é mais seguro do que abrir exceção no
	// guard, que é uma deny-list por prefixo.
	portal := e.Group("/patient")
	portal.GET("/activities/:id", h.patientActivity)
	portal.POST("/activities/:id/responses", h.submitResponse)
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
	var req SubmissionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}
	resp, err := h.svc.SubmitTyped(c.Request().Context(), assignmentID, req)
	if err != nil {
		return submissionError(err)
	}
	return c.JSON(http.StatusCreated, resp)
}

func (h *Handler) patientActivity(c echo.Context) error {
	assignmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	detail, err := h.svc.PatientActivity(c.Request().Context(), assignmentID)
	if err != nil {
		return submissionError(err)
	}
	return c.JSON(http.StatusOK, detail)
}

// submissionError mapeia os erros do fluxo da paciente. A mensagem de validação
// vai inteira para a tela: ela nomeia a pergunta e o motivo (regra A6 do guia).
func submissionError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidSubmission):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrPatientRequired):
		return echo.NewHTTPError(http.StatusForbidden, err.Error())
	case errors.Is(err, ErrAssignmentNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, ErrSubmissionNotAllowed), errors.Is(err, ErrAlreadySubmitted),
		errors.Is(err, ErrSubmissionVersionMismatch), errors.Is(err, ErrTemplateWithoutFields):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "falha ao submeter resposta")
	}
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

// --- Biblioteca de templates (ACO-66) ---

func (h *Handler) createTemplate(c echo.Context) error {
	var req TemplateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}
	t, err := h.svc.CreateTemplate(c.Request().Context(), req)
	if err != nil {
		return templateError(err, "falha ao criar template")
	}
	return c.JSON(http.StatusCreated, t)
}

func (h *Handler) getTemplate(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	t, err := h.svc.GetTemplate(c.Request().Context(), id)
	if err != nil {
		return templateError(err, "erro ao buscar template")
	}
	return c.JSON(http.StatusOK, t)
}

func (h *Handler) updateTemplate(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	var req TemplateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "corpo inválido")
	}
	t, err := h.svc.UpdateTemplate(c.Request().Context(), id, req)
	if err != nil {
		return templateError(err, "falha ao atualizar template")
	}
	return c.JSON(http.StatusOK, t)
}

func (h *Handler) archiveTemplate(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "id inválido")
	}
	t, err := h.svc.ArchiveTemplate(c.Request().Context(), id)
	if err != nil {
		return templateError(err, "falha ao arquivar template")
	}
	return c.JSON(http.StatusOK, t)
}

// templateError traduz os erros de domínio da biblioteca em status HTTP. As
// mensagens de validação (400) são específicas e vão direto para o usuário.
func templateError(err error, fallback string) error {
	switch {
	case errors.Is(err, ErrInvalidTemplate):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrPsychologistRequired), errors.Is(err, ErrTemplateReadOnly):
		return echo.NewHTTPError(http.StatusForbidden, err.Error())
	case errors.Is(err, ErrTemplateNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, ErrTemplateArchived), errors.Is(err, ErrTemplateSuperseded):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, fallback)
	}
}
