package patient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

var (
	ErrPatientOnly        = errors.New("rota disponível somente para paciente")
	ErrPortalUnavailable  = errors.New("contexto do paciente não disponível")
	ErrInvitationInvalid  = errors.New("convite inválido ou expirado")
	ErrInvitationConflict = errors.New("convite não pôde ser aceito")
	ErrInvalidMood        = errors.New("humor deve estar entre 1 e 5")
)

type InvitationRequest struct {
	Email string `json:"email"`
}

type InvitationResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type AcceptInvitationRequest struct {
	Password       string `json:"password"`
	ConsentVersion string `json:"consentVersion"`
}

type AcceptedPatient struct {
	UserID uuid.UUID `json:"userId"`
	Email  string    `json:"email"`
}

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

func (s *Service) CreateInvitation(ctx context.Context, patientID uuid.UUID, req InvitationRequest) (*InvitationResult, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPatientOnly
	}
	if strings.TrimSpace(req.Email) == "" {
		return nil, ErrInvitationInvalid
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrNotFound
	}
	if _, err := s.Get(ctx, patientID); err != nil {
		return nil, err
	}
	rel, err := tenant.Queries(ctx, s.q).CreatePendingRelationship(ctx, db.CreatePendingRelationshipParams{
		PatientID: patientID, PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	expiresAt := time.Now().Add(72 * time.Hour)
	_, err = tenant.Queries(ctx, s.q).CreatePatientInvitation(ctx, db.CreatePatientInvitationParams{
		PatientID: patientID, RelationshipID: rel.ID, Email: strings.ToLower(strings.TrimSpace(req.Email)),
		TokenHash: base64.RawURLEncoding.EncodeToString(hash[:]), ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, err
	}
	return &InvitationResult{Token: token, ExpiresAt: expiresAt}, nil
}

func (s *Service) AcceptInvitation(ctx context.Context, token string, req AcceptInvitationRequest) (*AcceptedPatient, error) {
	if len(req.Password) < 8 || strings.TrimSpace(req.ConsentVersion) == "" {
		return nil, ErrInvitationInvalid
	}
	hash := sha256.Sum256([]byte(token))
	row, err := tenant.Queries(ctx, s.q).GetInvitationForAcceptance(ctx, base64.RawURLEncoding.EncodeToString(hash[:]))
	if err != nil {
		return nil, ErrInvitationInvalid
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, ErrInvitationConflict
	}
	user, err := tenant.Queries(ctx, s.q).CreatePatientUser(ctx, db.CreatePatientUserParams{
		OrganizationID: &row.OrganizationID, Email: row.Email, PasswordHash: passwordHash,
	})
	if err != nil {
		return nil, ErrInvitationConflict
	}
	consentID, err := tenant.Queries(ctx, s.q).CreatePatientConsent(ctx, db.CreatePatientConsentParams{
		UserID: user.ID, Scope: "patient_portal", DocumentVersion: req.ConsentVersion,
	})
	if err != nil {
		return nil, ErrInvitationConflict
	}
	if err := tenant.Queries(ctx, s.q).LinkPatientUser(ctx, db.LinkPatientUserParams{UserID: &user.ID, PatientID: row.PatientID}); err != nil {
		return nil, ErrInvitationConflict
	}
	if err := tenant.Queries(ctx, s.q).AcceptPatientRelationship(ctx, db.AcceptPatientRelationshipParams{ConsentID: &consentID, ID: row.RelationshipID}); err != nil {
		return nil, ErrInvitationConflict
	}
	if err := tenant.Queries(ctx, s.q).MarkInvitationAccepted(ctx, row.ID); err != nil {
		return nil, ErrInvitationConflict
	}
	return &AcceptedPatient{UserID: user.ID, Email: user.Email}, nil
}

func (s *Service) patientID(ctx context.Context) (uuid.UUID, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "patient" {
		return uuid.Nil, ErrPatientOnly
	}
	row, err := tenant.Queries(ctx, s.q).GetPatientPortalContext(ctx, &id.UserID)
	if err != nil {
		return uuid.Nil, ErrPortalUnavailable
	}
	return row.ID, nil
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
	if _, err := s.patientID(ctx); err != nil {
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
	if _, err := s.patientID(ctx); err != nil {
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
	if _, err := s.patientID(ctx); err != nil {
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
	if _, err := s.patientID(ctx); err != nil {
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
	if _, err := s.patientID(ctx); err != nil {
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
