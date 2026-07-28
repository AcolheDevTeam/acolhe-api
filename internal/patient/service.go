// Package patient cobre o cadastro e a leitura de pacientes da organização.
package patient

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

var (
	// ErrNotFound: paciente inexistente ou fora da organização do requisitante.
	ErrNotFound = errors.New("paciente não encontrado")
	// ErrQueueUnavailable: fila não configurada (ex.: teste sem Redis).
	ErrQueueUnavailable = errors.New("fila de tarefas indisponível")
	// ErrInvalidInput indica dados inválidos no cadastro do paciente.
	ErrInvalidInput = errors.New("dados do paciente inválidos")
	// ErrPsychologistRequired indica que apenas psicólogos podem cadastrar pacientes.
	ErrPsychologistRequired = errors.New("perfil de psicólogo obrigatório")
)

type Service struct {
	q     db.Querier
	queue *asynq.Client
}

func NewService(q db.Querier, queue *asynq.Client) *Service {
	return &Service{q: q, queue: queue}
}

// RequestExport enfileira a exportação LGPD dos dados de um paciente (SLA 24h).
// O solicitante (RequestedBy) é o usuário autenticado; o paciente deve pertencer
// à organização do solicitante.
func (s *Service) RequestExport(ctx context.Context, patientID uuid.UUID) error {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return tenant.ErrNoTenant
	}
	if _, err := s.Get(ctx, patientID); err != nil {
		return err // ErrNotFound se fora da org
	}
	if s.queue == nil {
		return ErrQueueUnavailable
	}
	task, err := tasks.NewLGPDExportTask(tasks.LGPDExportPayload{
		PatientID:   patientID,
		RequestedBy: id.UserID,
	})
	if err != nil {
		return err
	}
	_, err = s.queue.EnqueueContext(ctx, task)
	return err
}

// Patient é a projeção pública de um paciente.
type Patient struct {
	ID        uuid.UUID `json:"id"`
	FullName  string    `json:"fullName"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateRequest é o corpo de POST /patients.
type CreateRequest struct {
	FullName  string  `json:"fullName"`
	BirthDate *string `json:"birthDate"`
}

// Create cadastra o paciente na organização e cria o vínculo com o psicólogo autenticado.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Patient, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	fullName := strings.TrimSpace(req.FullName)
	if len(fullName) < 2 || len(fullName) > 200 {
		return nil, ErrInvalidInput
	}

	birthDate := pgtype.Date{}
	if req.BirthDate != nil && *req.BirthDate != "" {
		parsed, err := time.Parse(time.DateOnly, *req.BirthDate)
		if err != nil || parsed.After(time.Now()) || parsed.Before(time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)) {
			return nil, ErrInvalidInput
		}
		birthDate = pgtype.Date{Time: parsed, Valid: true}
	}

	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	patientID := uuid.New()
	err = q.CreatePatient(ctx, db.CreatePatientParams{
		ID:             patientID,
		OrganizationID: id.OrgID,
		FullName:       fullName,
		BirthDate:      birthDate,
	})
	if err != nil {
		return nil, err
	}
	if err := q.CreatePatientRelationship(ctx, db.CreatePatientRelationshipParams{
		PatientID:      patientID,
		PsychologistID: psy.ID,
	}); err != nil {
		return nil, err
	}
	row, err := q.GetPatientForPsychologist(ctx, db.GetPatientForPsychologistParams{
		ID: patientID, OrganizationID: id.OrgID, PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	return &Patient{ID: row.ID, FullName: row.FullName, Status: row.Status, CreatedAt: row.CreatedAt}, nil
}

// List devolve os pacientes da organização do requisitante.
func (s *Service) List(ctx context.Context) ([]Patient, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	rows, err := q.ListPatientsByPsychologist(ctx, db.ListPatientsByPsychologistParams{
		OrganizationID: id.OrgID, PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Patient, 0, len(rows))
	for _, r := range rows {
		out = append(out, Patient{ID: r.ID, FullName: r.FullName, Status: r.Status, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// Get devolve um paciente da organização do requisitante.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Patient, error) {
	identity, ok := tenant.FromContext(ctx)
	if !ok || identity.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, identity.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	r, err := q.GetPatientForPsychologist(ctx, db.GetPatientForPsychologistParams{
		ID: id, OrganizationID: identity.OrgID, PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, ErrNotFound
	}
	return &Patient{ID: r.ID, FullName: r.FullName, Status: r.Status, CreatedAt: r.CreatedAt}, nil
}
