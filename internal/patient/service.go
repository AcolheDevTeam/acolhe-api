// Package patient cobre o cadastro e a leitura de pacientes da organização.
package patient

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

var (
	// ErrNotFound: paciente inexistente ou fora da organização do requisitante.
	ErrNotFound = errors.New("paciente não encontrado")
	// ErrQueueUnavailable: fila não configurada (ex.: teste sem Redis).
	ErrQueueUnavailable = errors.New("fila de tarefas indisponível")
)

type Service struct {
	q           db.Querier
	queue       *asynq.Client
	mailer      EmailSender
	frontendURL string
	tokenKey    string
	rate        *resendLimiter
}

func NewService(q db.Querier, queue *asynq.Client) *Service {
	return NewServiceWithDeps(q, queue, defaultMailer(), envOr("FRONTEND_URL", "http://localhost:3000"), envOr("INVITATION_TOKEN_KEY", "dev-invitation-key"))
}

func NewServiceWithDeps(q db.Querier, queue *asynq.Client, mailer EmailSender, frontendURL, tokenKey string) *Service {
	return &Service{q: q, queue: queue, mailer: mailer, frontendURL: frontendURL, tokenKey: tokenKey}
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
	ID                 uuid.UUID `json:"id"`
	FullName           string    `json:"fullName"`
	Status             string    `json:"status"`
	RelationshipStatus string    `json:"relationshipStatus"`
	CreatedAt          time.Time `json:"createdAt"`
	Email              string    `json:"email,omitempty"`
}

// List devolve os pacientes da organização do requisitante.
func (s *Service) List(ctx context.Context) ([]Patient, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tenant.Queries(ctx, s.q).ListPatientsByOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]Patient, 0, len(rows))
	for _, r := range rows {
		out = append(out, Patient{ID: r.ID, FullName: r.FullName, Status: r.Status, RelationshipStatus: r.RelationshipStatus, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// Get devolve um paciente da organização do requisitante.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Patient, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	r, err := tenant.Queries(ctx, s.q).GetPatient(ctx, db.GetPatientParams{ID: id, OrganizationID: orgID})
	if err != nil {
		return nil, ErrNotFound
	}
	return &Patient{ID: r.ID, FullName: r.FullName, Status: r.Status, RelationshipStatus: r.RelationshipStatus, CreatedAt: r.CreatedAt, Email: r.Email}, nil
}
