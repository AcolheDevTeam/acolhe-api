// Package activity cobre o sistema extensível de atividades: templates,
// atribuições (assignments) e respostas (responses) dos pacientes.
package activity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

var (
	// ErrAssignmentNotFound: atribuição inexistente ou fora da organização.
	ErrAssignmentNotFound = errors.New("atribuição não encontrada")
	// ErrTemplateNotFound: template inexistente ou não visível à organização.
	ErrTemplateNotFound = errors.New("template não encontrado")
	// ErrPatientNotFound: paciente inexistente ou fora da organização.
	ErrPatientNotFound = errors.New("paciente não encontrado")
	// ErrPsychologistRequired: atribuições exigem perfil de psicólogo.
	ErrPsychologistRequired = errors.New("perfil de psicólogo obrigatório")
	// ErrPatientRequired: respostas só podem ser submetidas pelo paciente vinculado.
	ErrPatientRequired = errors.New("perfil de paciente obrigatório")
	// ErrInvalidStatus: somente atividades submetidas podem ser revisadas.
	ErrInvalidStatus = errors.New("atividade precisa estar submetida para revisão")
	// ErrSubmissionNotAllowed: atividade já concluída ou indisponível para resposta.
	ErrSubmissionNotAllowed = errors.New("atividade não está disponível para resposta")
)

type Service struct {
	q db.Querier
}

func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

// Response é a projeção pública de uma resposta de atividade.
type Response struct {
	ID           uuid.UUID  `json:"id"`
	AssignmentID uuid.UUID  `json:"assignmentId"`
	SubmittedAt  *time.Time `json:"submittedAt"`
	IsDraft      bool       `json:"isDraft"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// Submit registra a resposta de uma atribuição e a marca como submetida.
// As duas escritas rodam na mesma transação da requisição (TenantTx).
func (s *Service) Submit(ctx context.Context, assignmentID uuid.UUID) (*Response, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "patient" {
		return nil, ErrPatientRequired
	}
	q := tenant.Queries(ctx, s.q)
	patient, err := q.GetPatientByUserInOrg(ctx, db.GetPatientByUserInOrgParams{
		UserID: &id.UserID, OrganizationID: id.OrgID,
	})
	if err != nil {
		return nil, ErrPatientRequired
	}
	assignment, err := q.GetAssignmentInOrg(ctx, db.GetAssignmentInOrgParams{ID: assignmentID, OrganizationID: id.OrgID})
	if err != nil || assignment.PatientID != patient.ID {
		return nil, ErrAssignmentNotFound
	}
	claimed, err := q.ClaimAssignmentForSubmission(ctx, db.ClaimAssignmentForSubmissionParams{
		ID: assignmentID, PatientID: patient.ID, OrganizationID: id.OrgID,
	})
	if err != nil {
		return nil, err
	}
	if claimed != 1 {
		return nil, ErrSubmissionNotAllowed
	}

	row, err := q.SubmitResponse(ctx, db.SubmitResponseParams{
		AssignmentID: assignmentID,
		SummaryScore: pgtype.Numeric{}, // sem pontuação automática neste passo
	})
	if err != nil {
		return nil, err
	}
	return &Response{ID: row.ID, AssignmentID: row.AssignmentID, SubmittedAt: row.SubmittedAt, IsDraft: row.IsDraft, CreatedAt: row.CreatedAt}, nil
}

// ListResponses devolve as respostas de uma atribuição (isolada por org).
func (s *Service) ListResponses(ctx context.Context, assignmentID uuid.UUID) ([]Response, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, tenant.ErrNoTenant
	}
	q := tenant.Queries(ctx, s.q)
	assignment, err := q.GetAssignmentInOrg(ctx, db.GetAssignmentInOrgParams{ID: assignmentID, OrganizationID: id.OrgID})
	if err != nil {
		return nil, ErrAssignmentNotFound
	}
	switch id.Role {
	case "psychologist":
		psy, err := q.GetPsychologistByUser(ctx, id.UserID)
		if err != nil || assignment.AssignerID != psy.ID {
			return nil, ErrAssignmentNotFound
		}
	case "patient":
		patient, err := q.GetPatientByUserInOrg(ctx, db.GetPatientByUserInOrgParams{
			UserID: &id.UserID, OrganizationID: id.OrgID,
		})
		if err != nil || assignment.PatientID != patient.ID {
			return nil, ErrAssignmentNotFound
		}
	default:
		return nil, ErrAssignmentNotFound
	}
	rows, err := q.ListResponsesByAssignment(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	out := make([]Response, 0, len(rows))
	for _, r := range rows {
		out = append(out, Response{ID: r.ID, AssignmentID: r.AssignmentID, SubmittedAt: r.SubmittedAt, IsDraft: r.IsDraft, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// Template é a projeção pública de um template de atividade.
type Template struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Type        string    `json:"type"`
	Description *string   `json:"description"`
	Version     int32     `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Assignment é a projeção pública de uma atribuição de atividade.
type Assignment struct {
	ID          uuid.UUID  `json:"id"`
	TemplateID  uuid.UUID  `json:"templateId"`
	PatientID   uuid.UUID  `json:"patientId"`
	PatientName string     `json:"patientName"`
	Title       string     `json:"title"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	DueAt       *time.Time `json:"dueAt"`
	RespondedAt *time.Time `json:"respondedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// AssignRequest é o corpo de POST /activities.
type AssignRequest struct {
	TemplateID uuid.UUID  `json:"templateId"`
	PatientID  uuid.UUID  `json:"patientId"`
	DueAt      *time.Time `json:"dueAt"`
}

// Assign cria uma atribuição usando o usuário autenticado como responsável.
func (s *Service) Assign(ctx context.Context, req AssignRequest) (*Assignment, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	patient, err := q.GetPatientForPsychologist(ctx, db.GetPatientForPsychologistParams{
		ID: req.PatientID, OrganizationID: id.OrgID, PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, ErrPatientNotFound
	}
	if _, err := q.GetActiveRelationship(ctx, db.GetActiveRelationshipParams{
		PatientID: req.PatientID, PsychologistID: psy.ID, OrganizationID: id.OrgID,
	}); err != nil {
		return nil, ErrPatientNotFound
	}
	template, err := q.GetActivityTemplateInOrg(ctx, db.GetActivityTemplateInOrgParams{
		ID: req.TemplateID, OrganizationID: &id.OrgID,
	})
	if err != nil {
		return nil, ErrTemplateNotFound
	}
	row, err := q.CreateAssignment(ctx, db.CreateAssignmentParams{
		TemplateID: req.TemplateID, TemplateVersion: template.Version,
		PatientID: req.PatientID, AssignerID: psy.ID, DueAt: req.DueAt,
	})
	if err != nil {
		return nil, err
	}
	return &Assignment{
		ID: row.ID, TemplateID: row.TemplateID, PatientID: row.PatientID,
		PatientName: patient.FullName, Title: template.Title, Type: template.Type, Status: row.Status,
		DueAt: row.DueAt, CreatedAt: row.CreatedAt,
	}, nil
}

// ListTemplates devolve os templates visíveis à organização (+ globais).
func (s *Service) ListTemplates(ctx context.Context) ([]Template, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tenant.Queries(ctx, s.q).ListActivityTemplates(ctx, &orgID)
	if err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(rows))
	for _, r := range rows {
		out = append(out, Template{ID: r.ID, Title: r.Title, Type: r.Type, Description: r.Description, Version: r.Version, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// ListAssignments devolve as atribuições de um paciente, isoladas por org.
func (s *Service) ListAssignments(ctx context.Context, patientID uuid.UUID) ([]Assignment, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	rows, err := tenant.Queries(ctx, s.q).ListAssignmentsByPatient(ctx, db.ListAssignmentsByPatientParams{
		PatientID:      patientID,
		OrganizationID: id.OrgID,
		AssignerID:     psy.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, r := range rows {
		out = append(out, Assignment{
			ID: r.ID, TemplateID: r.TemplateID, PatientID: r.PatientID,
			PatientName: r.PatientName, Title: r.Title, Type: r.Type, Status: r.Status,
			DueAt: r.DueAt, RespondedAt: r.RespondedAt, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// ListAll devolve todas as atribuições da organização.
func (s *Service) ListAll(ctx context.Context) ([]Assignment, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	rows, err := tenant.Queries(ctx, s.q).ListAssignmentsByPsychologist(ctx, db.ListAssignmentsByPsychologistParams{
		OrganizationID: id.OrgID, AssignerID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, r := range rows {
		out = append(out, Assignment{
			ID: r.ID, TemplateID: r.TemplateID, PatientID: r.PatientID,
			PatientName: r.PatientName, Title: r.Title, Type: r.Type, Status: r.Status,
			DueAt: r.DueAt, RespondedAt: r.RespondedAt, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// Get devolve uma atribuição da organização.
func (s *Service) Get(ctx context.Context, assignmentID uuid.UUID) (*Assignment, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	r, err := tenant.Queries(ctx, s.q).GetAssignmentDetailInOrg(ctx, db.GetAssignmentDetailInOrgParams{
		ID: assignmentID, OrganizationID: id.OrgID, AssignerID: psy.ID,
	})
	if err != nil {
		return nil, ErrAssignmentNotFound
	}
	return &Assignment{
		ID: r.ID, TemplateID: r.TemplateID, PatientID: r.PatientID,
		PatientName: r.PatientName, Title: r.Title, Type: r.Type, Status: r.Status,
		DueAt: r.DueAt, RespondedAt: r.RespondedAt, CreatedAt: r.CreatedAt,
	}, nil
}

// MarkReviewed conclui a revisão de uma atividade submetida.
func (s *Service) MarkReviewed(ctx context.Context, assignmentID uuid.UUID) (*Assignment, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	current, err := q.GetAssignmentDetailInOrg(ctx, db.GetAssignmentDetailInOrgParams{
		ID: assignmentID, OrganizationID: id.OrgID, AssignerID: psy.ID,
	})
	if err != nil {
		return nil, ErrAssignmentNotFound
	}
	if current.Status != "submitted" {
		return nil, ErrInvalidStatus
	}
	updated, err := q.MarkAssignmentReviewed(ctx, db.MarkAssignmentReviewedParams{
		ID: assignmentID, OrganizationID: id.OrgID, AssignerID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	if updated != 1 {
		return nil, ErrInvalidStatus
	}
	return s.Get(ctx, assignmentID)
}
