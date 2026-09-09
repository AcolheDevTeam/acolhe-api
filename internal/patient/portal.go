package patient

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

var (
	ErrPatientOnly       = errors.New("rota disponível somente para paciente")
	ErrPortalUnavailable = errors.New("contexto do paciente não disponível")
	ErrInvalidMood       = errors.New("humor deve estar entre 1 e 5")
)

type PortalContext struct {
	PatientID          uuid.UUID `json:"patientId"`
	FullName           string    `json:"fullName"`
	RelationshipStatus string    `json:"relationshipStatus"`
	Consented          bool      `json:"consented"`
}

type NextSession struct {
	ID              uuid.UUID `json:"id"`
	ScheduledFor    time.Time `json:"scheduledFor"`
	DurationMinutes int32     `json:"durationMinutes"`
	Modality        string    `json:"modality"`
	Status          string    `json:"status"`
}

type PendingActivity struct {
	ID           uuid.UUID  `json:"id"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	ScheduledFor *time.Time `json:"scheduledFor"`
	DueAt        *time.Time `json:"dueAt"`
}

type ProcessSummary struct {
	SessionCount         int32 `json:"sessionCount"`
	PendingActivityCount int32 `json:"pendingActivityCount"`
	CheckinCount         int32 `json:"checkinCount"`
}

type PatientCheckin struct {
	ID        uuid.UUID `json:"id"`
	Mood      int32     `json:"mood"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
}

type CheckinRequest struct {
	Mood int32   `json:"mood"`
	Note *string `json:"note"`
}

// requirePatient garante que o requisitante é um paciente com contexto de portal
// resolvível; as queries patient-scoped usam o user_id do contexto, nunca um
// patientId vindo do cliente.
func (s *Service) requirePatient(ctx context.Context) error {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "patient" {
		return ErrPatientOnly
	}
	if _, err := tenant.Queries(ctx, s.q).GetPatientPortalContext(ctx, &id.UserID); err != nil {
		return ErrPortalUnavailable
	}
	return nil
}

func (s *Service) PortalContext(ctx context.Context) (*PortalContext, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "patient" {
		return nil, ErrPatientOnly
	}
	row, err := tenant.Queries(ctx, s.q).GetPatientPortalContext(ctx, &id.UserID)
	if err != nil {
		return nil, ErrPortalUnavailable
	}
	return &PortalContext{PatientID: row.ID, FullName: row.FullName, RelationshipStatus: row.RelationshipStatus, Consented: row.Consented}, nil
}

func (s *Service) NextSession(ctx context.Context) (*NextSession, error) {
	if err := s.requirePatient(ctx); err != nil {
		return nil, err
	}
	id, _ := tenant.FromContext(ctx)
	row, err := tenant.Queries(ctx, s.q).GetPatientNextAppointment(ctx, &id.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &NextSession{ID: row.ID, ScheduledFor: row.ScheduledFor, DurationMinutes: row.DurationMinutes, Modality: row.Modality, Status: row.Status}, nil
}

func (s *Service) PendingActivities(ctx context.Context) ([]PendingActivity, error) {
	if err := s.requirePatient(ctx); err != nil {
		return nil, err
	}
	id, _ := tenant.FromContext(ctx)
	rows, err := tenant.Queries(ctx, s.q).ListPatientPendingActivities(ctx, &id.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]PendingActivity, 0, len(rows))
	for _, row := range rows {
		out = append(out, PendingActivity{ID: row.ID, Title: row.Title, Status: row.Status, ScheduledFor: row.ScheduledFor, DueAt: row.DueAt})
	}
	return out, nil
}

func (s *Service) Checkins(ctx context.Context) ([]PatientCheckin, error) {
	if err := s.requirePatient(ctx); err != nil {
		return nil, err
	}
	id, _ := tenant.FromContext(ctx)
	rows, err := tenant.Queries(ctx, s.q).ListPatientCheckins(ctx, &id.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]PatientCheckin, 0, len(rows))
	for _, row := range rows {
		out = append(out, PatientCheckin{ID: row.ID, Mood: row.Mood, Note: row.Note, CreatedAt: row.CreatedAt})
	}
	return out, nil
}

func (s *Service) CreatePatientCheckin(ctx context.Context, req CheckinRequest) (*PatientCheckin, error) {
	if req.Mood < 1 || req.Mood > 5 {
		return nil, ErrInvalidMood
	}
	if err := s.requirePatient(ctx); err != nil {
		return nil, err
	}
	id, _ := tenant.FromContext(ctx)
	row, err := tenant.Queries(ctx, s.q).CreatePatientCheckin(ctx, db.CreatePatientCheckinParams{Mood: req.Mood, Note: req.Note, UserID: &id.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPortalUnavailable
	}
	if err != nil {
		return nil, err
	}
	return &PatientCheckin{ID: row.ID, Mood: row.Mood, Note: row.Note, CreatedAt: row.CreatedAt}, nil
}

func (s *Service) ProcessSummary(ctx context.Context) (*ProcessSummary, error) {
	if err := s.requirePatient(ctx); err != nil {
		return nil, err
	}
	id, _ := tenant.FromContext(ctx)
	row, err := tenant.Queries(ctx, s.q).GetPatientProcessSummary(ctx, &id.UserID)
	if err != nil {
		return nil, err
	}
	return &ProcessSummary{SessionCount: row.SessionCount, PendingActivityCount: row.PendingActivityCount, CheckinCount: row.CheckinCount}, nil
}

func portalError(err error) *echo.HTTPError {
	if errors.Is(err, ErrPatientOnly) {
		return echo.NewHTTPError(http.StatusForbidden, "acesso não permitido")
	}
	if errors.Is(err, ErrPortalUnavailable) || errors.Is(err, ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "conteúdo não disponível")
	}
	return echo.NewHTTPError(http.StatusInternalServerError, "não foi possível carregar o conteúdo")
}
