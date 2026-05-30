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

// ErrAssignmentNotFound: atribuição inexistente ou fora da organização.
var ErrAssignmentNotFound = errors.New("atribuição não encontrada")

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
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	q := tenant.Queries(ctx, s.q)

	if _, err := q.GetAssignmentInOrg(ctx, db.GetAssignmentInOrgParams{ID: assignmentID, OrganizationID: orgID}); err != nil {
		return nil, ErrAssignmentNotFound
	}

	row, err := q.SubmitResponse(ctx, db.SubmitResponseParams{
		AssignmentID: assignmentID,
		SummaryScore: pgtype.Numeric{}, // sem pontuação automática neste passo
	})
	if err != nil {
		return nil, err
	}
	if err := q.MarkAssignmentSubmitted(ctx, assignmentID); err != nil {
		return nil, err
	}
	return &Response{ID: row.ID, AssignmentID: row.AssignmentID, SubmittedAt: row.SubmittedAt, IsDraft: row.IsDraft, CreatedAt: row.CreatedAt}, nil
}

// ListResponses devolve as respostas de uma atribuição (isolada por org).
func (s *Service) ListResponses(ctx context.Context, assignmentID uuid.UUID) ([]Response, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	q := tenant.Queries(ctx, s.q)
	if _, err := q.GetAssignmentInOrg(ctx, db.GetAssignmentInOrgParams{ID: assignmentID, OrganizationID: orgID}); err != nil {
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
	Description *string   `json:"description"`
	Version     int32     `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Assignment é a projeção pública de uma atribuição de atividade.
type Assignment struct {
	ID           uuid.UUID  `json:"id"`
	TemplateID   uuid.UUID  `json:"templateId"`
	PatientID    uuid.UUID  `json:"patientId"`
	Status       string     `json:"status"`
	ScheduledFor *time.Time `json:"scheduledFor"`
	DueAt        *time.Time `json:"dueAt"`
	CreatedAt    time.Time  `json:"createdAt"`
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
		out = append(out, Template{ID: r.ID, Title: r.Title, Description: r.Description, Version: r.Version, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// ListAssignments devolve as atribuições de um paciente, isoladas por org.
func (s *Service) ListAssignments(ctx context.Context, patientID uuid.UUID) ([]Assignment, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tenant.Queries(ctx, s.q).ListAssignmentsByPatient(ctx, db.ListAssignmentsByPatientParams{
		PatientID:      patientID,
		OrganizationID: orgID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, r := range rows {
		out = append(out, Assignment{
			ID:           r.ID,
			TemplateID:   r.TemplateID,
			PatientID:    r.PatientID,
			Status:       r.Status,
			ScheduledFor: r.ScheduledFor,
			DueAt:        r.DueAt,
			CreatedAt:    r.CreatedAt,
		})
	}
	return out, nil
}
