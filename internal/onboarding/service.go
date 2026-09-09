// Package onboarding owns the public, token-scoped patient invitation flow.
package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

var (
	ErrInvitationNotFound = errors.New("convite não encontrado")
	ErrInvitationGone     = errors.New("convite expirado ou indisponível")
	ErrInvalidInput       = errors.New("dados de aceite inválidos")
	ErrRequiredConsent    = errors.New("consentimento obrigatório ausente")
	ErrEmailInUse         = errors.New("e-mail já cadastrado")
)

type Service struct {
	q db.Querier
}

func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

type ConsentDocument struct {
	ID            uuid.UUID `json:"id"`
	Scope         string    `json:"scope"`
	Version       string    `json:"version"`
	Title         string    `json:"title"`
	Content       string    `json:"content"`
	ContentSHA256 string    `json:"contentSha256"`
	Required      bool      `json:"required"`
	PublishedAt   time.Time `json:"publishedAt"`
}

type Invitation struct {
	PatientName      string            `json:"patientName"`
	PsychologistName string            `json:"psychologistName"`
	PsychologistCRP  string            `json:"psychologistCrp"`
	Email            string            `json:"email"`
	ExpiresAt        time.Time         `json:"expiresAt"`
	Documents        []ConsentDocument `json:"documents"`
}

type AcceptRequest struct {
	Password            string      `json:"password"`
	AcceptedDocumentIDs []uuid.UUID `json:"acceptedDocumentIds"`
}

type Acceptance struct {
	PatientID       uuid.UUID `json:"patientId"`
	UserID          uuid.UUID `json:"userId"`
	RelationshipID  uuid.UUID `json:"relationshipId"`
	AlreadyAccepted bool      `json:"alreadyAccepted"`
}

func (s *Service) Get(ctx context.Context, token string) (*Invitation, error) {
	digest, err := tokenDigest(token)
	if err != nil {
		return nil, ErrInvitationNotFound
	}
	row, err := s.q.GetInvitationByDigest(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvitationNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.Status != "pending" || !row.ExpiresAt.After(time.Now()) {
		return nil, ErrInvitationGone
	}
	rows, err := s.q.ListPublishedConsentDocuments(ctx)
	if err != nil {
		return nil, err
	}
	documents := make([]ConsentDocument, 0, len(rows))
	for _, document := range rows {
		documents = append(documents, ConsentDocument{
			ID: document.ID, Scope: document.Scope, Version: document.Version,
			Title: document.Title, Content: document.Content,
			ContentSHA256: document.ContentSha256, Required: document.Required,
			PublishedAt: document.PublishedAt,
		})
	}
	return &Invitation{
		PatientName: row.PatientName, PsychologistName: row.PsychologistName,
		PsychologistCRP: row.CrpState + " " + row.CrpNumber,
		Email:           row.Email, ExpiresAt: row.ExpiresAt, Documents: documents,
	}, nil
}

func (s *Service) Accept(ctx context.Context, token string, req AcceptRequest, ip netip.Addr, userAgent string) (*Acceptance, error) {
	digest, err := tokenDigest(token)
	if err != nil {
		return nil, ErrInvitationNotFound
	}
	if len(req.Password) < 8 || len(req.Password) > 72 || len(req.AcceptedDocumentIDs) == 0 {
		return nil, ErrInvalidInput
	}
	for _, id := range req.AcceptedDocumentIDs {
		if id == uuid.Nil {
			return nil, ErrInvalidInput
		}
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	row, err := s.q.AcceptPatientInvitation(ctx, db.AcceptPatientInvitationParams{
		TokenDigest: digest, PasswordHash: hash,
		AcceptedDocumentIds: req.AcceptedDocumentIDs,
		IpAddress:           ip, UserAgent: truncate(strings.TrimSpace(userAgent), 500),
	})
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	return &Acceptance{
		PatientID: row.PatientID, UserID: row.UserID,
		RelationshipID: row.RelationshipID, AlreadyAccepted: row.AlreadyAccepted,
	}, nil
}

func (s *Service) Decline(ctx context.Context, token string, ip netip.Addr, userAgent string) error {
	digest, err := tokenDigest(token)
	if err != nil {
		return ErrInvitationNotFound
	}
	_, err = s.q.DeclinePatientInvitation(ctx, db.DeclinePatientInvitationParams{
		TokenDigest: digest, IpAddress: ip,
		UserAgent: truncate(strings.TrimSpace(userAgent), 500),
	})
	if err != nil {
		return mapDatabaseError(err)
	}
	return nil
}

func tokenDigest(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, ErrInvitationNotFound
	}
	digest := sha256.Sum256(raw)
	return digest[:], nil
}

func mapDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "P0002":
		return ErrInvitationNotFound
	case "22023", "55000":
		return ErrInvitationGone
	case "23514":
		return ErrRequiredConsent
	case "23505":
		return ErrEmailInUse
	default:
		return err
	}
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
