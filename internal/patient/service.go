// Package patient cobre o cadastro e a leitura de pacientes da organização.
package patient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/mailer"
	taskqueue "github.com/joycesilva/acolhe-api/internal/queue"
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
	q           db.Querier
	queue       taskqueue.Enqueuer
	mailer      mailer.Mailer // nil = envio de convite desabilitado
	frontendURL string
}

// NewService monta o domínio de pacientes. Opções (ex.: WithInvitationMailer)
// habilitam integrações sem obrigar os testes a configurá-las.
func NewService(q db.Querier, queue taskqueue.Enqueuer, opts ...Option) *Service {
	s := &Service{q: q, queue: queue}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RequestExport enfileira a exportação LGPD dos dados de um paciente (SLA 24h).
// O solicitante (RequestedBy) é o usuário autenticado; o paciente deve pertencer
// à organização do solicitante.
func (s *Service) RequestExport(ctx context.Context, patientID uuid.UUID) error {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return tenant.ErrNoTenant
	}
	q := tenant.Queries(ctx, s.q)
	if _, err := q.GetPatientExportAccess(ctx, db.GetPatientExportAccessParams{
		RequestedBy: id.UserID, PatientID: patientID, OrganizationID: id.OrgID,
	}); err != nil {
		return ErrNotFound
	}
	if s.queue == nil {
		return ErrQueueUnavailable
	}

	requestID := uuid.New()
	requestedAt := time.Now().UTC()
	if _, err := q.CreateLGPDExportRequest(ctx, db.CreateLGPDExportRequestParams{
		ID: requestID, PatientID: patientID, OrganizationID: id.OrgID,
		RequestedBy: id.UserID, RequestedAt: requestedAt,
		SlaDeadline: requestedAt.Add(24 * time.Hour),
	}); err != nil {
		return err
	}

	task, err := tasks.NewLGPDExportTask(tasks.LGPDExportPayload{
		RequestID: requestID, PatientID: patientID, OrganizationID: id.OrgID,
		RequestedBy: id.UserID, RequesterRole: id.Role, RequestedAt: requestedAt,
	})
	if err != nil {
		return err
	}
	_, err = s.queue.EnqueueContext(ctx, task)
	if err != nil {
		message := err.Error()
		_, _ = q.MarkLGPDExportQueueFailed(ctx, db.MarkLGPDExportQueueFailedParams{
			LastError: &message, ID: requestID, RequestedBy: id.UserID,
		})
	}
	return err
}

// Patient é a projeção pública de um paciente.
type Patient struct {
	ID                 uuid.UUID   `json:"id"`
	FullName           string      `json:"fullName"`
	Status             string      `json:"status"`
	RelationshipStatus string      `json:"relationshipStatus"`
	Invitation         *Invitation `json:"invitation,omitempty"`
	CreatedAt          time.Time   `json:"createdAt"`
}

// Invitation is returned only when a psychologist creates or deliberately
// reissues a patient invitation. The opaque token is never persisted.
type Invitation struct {
	Token     string    `json:"token"`
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"expiresAt"`
	// DeliveryStatus informa o resultado do e-mail: sent, failed ou disabled.
	// O token é devolvido sempre, para o link copiável servir de fallback.
	DeliveryStatus string `json:"deliveryStatus"`
}

// CreateRequest é o corpo de POST /patients.
type CreateRequest struct {
	FullName       string    `json:"fullName"`
	Email          string    `json:"email"`
	BirthDate      *string   `json:"birthDate"`
	IdempotencyKey uuid.UUID `json:"-"`
}

// Create cadastra o paciente na organização e cria o vínculo com o psicólogo autenticado.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Patient, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	fullName := strings.TrimSpace(req.FullName)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	parsedEmail, emailErr := mail.ParseAddress(email)
	if len(fullName) < 2 || len(fullName) > 200 || emailErr != nil ||
		parsedEmail.Address != email || len(email) > 320 {
		return nil, ErrInvalidInput
	}
	if req.IdempotencyKey == uuid.Nil {
		req.IdempotencyKey = uuid.New()
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
	if _, err := q.LockPatientCreationKey(ctx, req.IdempotencyKey.String()); err != nil {
		return nil, err
	}

	token, digest, err := newInvitationToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)

	existing, err := q.GetInvitationByCreationKey(ctx, db.GetInvitationByCreationKeyParams{
		CreatedByUserID: id.UserID,
		IdempotencyKey:  req.IdempotencyKey,
	})
	if err == nil {
		if existing.Email != email || existing.Status != "pending" {
			return nil, ErrInvalidInput
		}
		reissued, err := q.ReissuePatientInvitation(ctx, db.ReissuePatientInvitationParams{
			ID: existing.ID, TokenDigest: digest, ExpiresAt: expiresAt,
		})
		if err != nil {
			return nil, err
		}
		row, err := q.GetPatientForPsychologist(ctx, db.GetPatientForPsychologistParams{
			ID: existing.PatientID, OrganizationID: id.OrgID, PsychologistID: psy.ID,
		})
		if err != nil {
			return nil, err
		}
		invitation := &Invitation{Token: token, Email: email, ExpiresAt: reissued.ExpiresAt}
		s.deliverInvitation(ctx, row.ID, invitation, row.FullName, psy.FullName)
		return toPatient(row.ID, row.FullName, row.Status, row.RelationshipStatus, row.CreatedAt, invitation), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
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
	relationshipID, err := q.CreatePatientRelationship(ctx, db.CreatePatientRelationshipParams{
		PatientID:      patientID,
		PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	invitation, err := q.CreatePatientInvitation(ctx, db.CreatePatientInvitationParams{
		PatientID: patientID, RelationshipID: relationshipID, Email: email,
		TokenDigest: digest, IdempotencyKey: req.IdempotencyKey,
		CreatedByUserID: id.UserID, ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, err
	}
	row, err := q.GetPatientForPsychologist(ctx, db.GetPatientForPsychologistParams{
		ID: patientID, OrganizationID: id.OrgID, PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	delivery := &Invitation{Token: token, Email: email, ExpiresAt: invitation.ExpiresAt}
	s.deliverInvitation(ctx, row.ID, delivery, row.FullName, psy.FullName)
	return toPatient(row.ID, row.FullName, row.Status, row.RelationshipStatus, row.CreatedAt, delivery), nil
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
		out = append(out, *toPatient(r.ID, r.FullName, r.Status, r.RelationshipStatus, r.CreatedAt, nil))
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
	return toPatient(r.ID, r.FullName, r.Status, r.RelationshipStatus, r.CreatedAt, nil), nil
}

// ReissueInvitation replaces the opaque token for a pending patient invitation.
// Only the psychologist who owns the relationship can issue the replacement;
// the previous link stops working as soon as the digest is updated.
func (s *Service) ReissueInvitation(ctx context.Context, patientID uuid.UUID) (*Invitation, error) {
	identity, ok := tenant.FromContext(ctx)
	if !ok || identity.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}

	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, identity.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	pending, err := q.GetReissuableInvitationForPatient(ctx, db.GetReissuableInvitationForPatientParams{
		PatientID: patientID, OrganizationID: identity.OrgID, PsychologistID: psy.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	token, digest, err := newInvitationToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)
	reissued, err := q.ReissuePatientInvitation(ctx, db.ReissuePatientInvitationParams{
		ID: pending.ID, TokenDigest: digest, ExpiresAt: expiresAt,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	invitation := &Invitation{Token: token, Email: pending.Email, ExpiresAt: reissued.ExpiresAt}
	if s.mailer == nil {
		invitation.DeliveryStatus = DeliveryDisabled
		return invitation, nil
	}
	// O nome só é necessário para o texto do e-mail; a consulta reaproveita o
	// predicado de isolamento (org + psicóloga) e falha silenciosa vira saudação genérica.
	patientName := ""
	if row, err := q.GetPatientForPsychologist(ctx, db.GetPatientForPsychologistParams{
		ID: patientID, OrganizationID: identity.OrgID, PsychologistID: psy.ID,
	}); err == nil {
		patientName = row.FullName
	}
	s.deliverInvitation(ctx, patientID, invitation, patientName, psy.FullName)
	return invitation, nil
}

func newInvitationToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), digest[:], nil
}

func toPatient(id uuid.UUID, fullName, status, relationshipStatus string, createdAt time.Time, invitation *Invitation) *Patient {
	return &Patient{
		ID: id, FullName: fullName, Status: status,
		RelationshipStatus: relationshipStatus, Invitation: invitation, CreatedAt: createdAt,
	}
}
