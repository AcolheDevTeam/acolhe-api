// Package activity cobre o sistema extensível de atividades: templates,
// atribuições (assignments) e respostas (responses) dos pacientes.
package activity

import (
	"context"
	"encoding/json"
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
	// ErrInvalidResponse: resposta ausente, incompleta ou inconsistente com o template.
	ErrInvalidResponse = errors.New("resposta submetida não está íntegra")
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

// ReviewField exposes exactly one typed answer in template display order.
type ReviewField struct {
	FieldID      uuid.UUID       `json:"fieldId"`
	Code         string          `json:"code"`
	Label        string          `json:"label"`
	FieldType    string          `json:"fieldType"`
	DisplayOrder int32           `json:"displayOrder"`
	Config       json.RawMessage `json:"config"`
	Kind         string          `json:"kind"`
	Value        any             `json:"value"`
}

type AttachmentValue struct {
	ID        uuid.UUID `json:"id"`
	MimeType  string    `json:"mimeType"`
	SizeBytes int32     `json:"sizeBytes"`
}

type ActivitySubmission struct {
	ID          uuid.UUID     `json:"id"`
	SubmittedAt time.Time     `json:"submittedAt"`
	Fields      []ReviewField `json:"fields"`
}

// ActivityReviewDetail is a closed state machine at the HTTP boundary. Only
// state=submitted is reviewable; invalid legacy rows fail closed.
type ActivityReviewDetail struct {
	State           string              `json:"state"`
	ID              uuid.UUID           `json:"id"`
	TemplateID      uuid.UUID           `json:"templateId"`
	TemplateVersion int32               `json:"templateVersion"`
	PatientID       uuid.UUID           `json:"patientId"`
	PatientName     string              `json:"patientName"`
	Title           string              `json:"title"`
	Type            string              `json:"type"`
	Status          string              `json:"status"`
	DueAt           *time.Time          `json:"dueAt"`
	FieldCount      int32               `json:"fieldCount"`
	CreatedAt       time.Time           `json:"createdAt"`
	ReviewedAt      *time.Time          `json:"reviewedAt,omitempty"`
	Submission      *ActivitySubmission `json:"submission,omitempty"`
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

// Get returns review metadata and, only when complete, every typed answer in
// template order.
func (s *Service) Get(ctx context.Context, assignmentID uuid.UUID) (*ActivityReviewDetail, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	q := tenant.Queries(ctx, s.q)
	r, err := q.GetActivityReviewMetadata(ctx, db.GetActivityReviewMetadataParams{
		ID: assignmentID, OrganizationID: id.OrgID, AssignerID: psy.ID,
	})
	if err != nil {
		return nil, ErrAssignmentNotFound
	}
	detail := &ActivityReviewDetail{
		ID: r.ID, TemplateID: r.TemplateID, TemplateVersion: r.TemplateVersion,
		PatientID: r.PatientID, PatientName: r.PatientName,
		Title: r.Title, Type: r.Type, Status: r.Status, DueAt: r.DueAt,
		FieldCount: r.FieldCount, CreatedAt: r.CreatedAt, ReviewedAt: r.ReviewedAt,
	}

	if r.SubmissionComplete && r.ResponseID != "" && r.SubmittedAt != nil {
		responseID, parseErr := uuid.Parse(r.ResponseID)
		if parseErr != nil {
			detail.State = "submission_invalid"
			return detail, nil
		}
		rows, valuesErr := q.ListActivityReviewValues(ctx, db.ListActivityReviewValuesParams{
			ResponseID: responseID, AssignmentID: assignmentID,
			OrganizationID: id.OrgID, AssignerID: psy.ID,
		})
		if valuesErr != nil || len(rows) != int(r.FieldCount) {
			detail.State = "submission_invalid"
			return detail, nil
		}
		fields := make([]ReviewField, 0, len(rows))
		for _, row := range rows {
			field, conversionErr := reviewField(row)
			if conversionErr != nil {
				detail.State = "submission_invalid"
				return detail, nil
			}
			fields = append(fields, field)
		}
		detail.Submission = &ActivitySubmission{
			ID: responseID, SubmittedAt: *r.SubmittedAt, Fields: fields,
		}
		switch r.Status {
		case "submitted":
			detail.State = "submitted"
		case "reviewed":
			if r.ReviewedAt == nil {
				detail.State = "submission_invalid"
				detail.Submission = nil
			} else {
				detail.State = "reviewed"
			}
		default:
			detail.State = "submission_invalid"
			detail.Submission = nil
		}
		return detail, nil
	}

	switch r.Status {
	case "pending", "in_progress":
		if r.ResponseID == "" {
			detail.State = "awaiting_response"
		} else {
			detail.State = "submission_invalid"
		}
	case "expired", "canceled":
		if r.ResponseID == "" {
			detail.State = "closed_without_submission"
		} else {
			detail.State = "submission_invalid"
		}
	default:
		detail.State = "submission_invalid"
	}
	return detail, nil
}

// MarkReviewed is an idempotent domain command. The SQL predicate repeats
// ownership and complete-submission integrity before changing state.
func (s *Service) MarkReviewed(ctx context.Context, assignmentID uuid.UUID) (*ActivityReviewDetail, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	current, err := s.Get(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	if current.State == "reviewed" {
		return current, nil
	}
	if current.State != "submitted" {
		return nil, ErrInvalidStatus
	}
	updated, err := q.MarkCompleteAssignmentReviewed(ctx, db.MarkCompleteAssignmentReviewedParams{
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

func reviewField(row db.ListActivityReviewValuesRow) (ReviewField, error) {
	config := json.RawMessage(row.Config)
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	field := ReviewField{
		FieldID: row.FieldID, Code: row.FieldCode, Label: row.Label,
		FieldType: row.FieldType, DisplayOrder: row.DisplayOrder, Config: config,
	}
	switch {
	case row.ValueText != nil:
		field.Kind, field.Value = "text", *row.ValueText
	case row.ValueNumber.Valid:
		number, err := row.ValueNumber.Float64Value()
		if err != nil || !number.Valid {
			return ReviewField{}, ErrInvalidResponse
		}
		field.Kind, field.Value = "number", number.Float64
	case row.ValueBoolean != nil:
		field.Kind, field.Value = "boolean", *row.ValueBoolean
	case row.ValueDatetime != nil:
		field.Kind, field.Value = "datetime", *row.ValueDatetime
	case len(row.ValueJson) > 0:
		if !json.Valid(row.ValueJson) {
			return ReviewField{}, ErrInvalidResponse
		}
		field.Kind, field.Value = "json", json.RawMessage(row.ValueJson)
	case row.AttachmentID != nil && row.MimeType != nil && row.SizeBytes != nil:
		field.Kind, field.Value = "attachment", AttachmentValue{
			ID: *row.AttachmentID, MimeType: *row.MimeType, SizeBytes: *row.SizeBytes,
		}
	default:
		return ReviewField{}, ErrInvalidResponse
	}
	return field, nil
}
