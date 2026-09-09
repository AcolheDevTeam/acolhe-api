package patient

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/invitation"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"golang.org/x/crypto/bcrypt"
)

const invitationTTL = 72 * time.Hour

var (
	ErrInviteNotFound    = errors.New("convite não encontrado")
	ErrInviteRateLimited = errors.New("limite de reenvios atingido")
)

type InvitationResult struct {
	Patient    Patient          `json:"patient"`
	Invitation InvitationStatus `json:"invitation"`
	CopyLink   string           `json:"copyLink"`
}

type InvitationStatus struct {
	ID             uuid.UUID `json:"id"`
	Status         string    `json:"status"`
	DeliveryStatus string    `json:"deliveryStatus"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type resendLimiter struct {
	mu    sync.Mutex
	last  map[uuid.UUID]time.Time
	count map[uuid.UUID]int
}

func (s *Service) limiter() *resendLimiter {
	if s.rate == nil {
		s.rate = &resendLimiter{last: map[uuid.UUID]time.Time{}, count: map[uuid.UUID]int{}}
	}
	return s.rate
}

func (s *Service) createInvitation(ctx context.Context, patient Patient, email string) (*InvitationResult, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, tenant.ErrNoTenant
	}
	token, hash, err := invitation.NewToken()
	if err != nil {
		return nil, err
	}
	ciphertext, err := invitation.Encrypt(token, s.tokenKey)
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(invitationTTL)
	row, err := tenant.Queries(ctx, s.q).CreateInvitation(ctx, db.CreateInvitationParams{PatientID: patient.ID, OrganizationID: id.OrgID, Email: email, TokenHash: hash, TokenCiphertext: ciphertext, ExpiresAt: expires})
	if err != nil {
		return nil, err
	}
	if s.queue == nil {
		return nil, ErrQueueUnavailable
	}
	task, err := tasks.NewInvitationEmailTask(tasks.InvitationEmailPayload{InvitationID: row.ID})
	if err != nil {
		return nil, err
	}
	if _, err = s.queue.EnqueueContext(ctx, task, asynq.TaskID(row.ID.String())); err != nil {
		return nil, err
	}
	return &InvitationResult{Patient: patient, Invitation: InvitationStatus{ID: row.ID, Status: row.Status, DeliveryStatus: row.DeliveryStatus, ExpiresAt: expires}, CopyLink: s.link(token)}, nil
}

func (s *Service) Create(ctx context.Context, name, email string) (*InvitationResult, error) {
	org, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	id, _ := tenant.FromContext(ctx)
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, err
	}
	row, err := tenant.Queries(ctx, s.q).CreatePatient(ctx, db.CreatePatientParams{OrganizationID: org, FullName: strings.TrimSpace(name), Email: strings.ToLower(strings.TrimSpace(email))})
	if err != nil {
		return nil, err
	}
	if err = tenant.Queries(ctx, s.q).CreatePatientRelationship(ctx, db.CreatePatientRelationshipParams{PatientID: row.ID, PsychologistID: psy.ID}); err != nil {
		return nil, err
	}
	return s.createInvitation(ctx, Patient{ID: row.ID, FullName: row.FullName, Status: row.Status, CreatedAt: row.CreatedAt}, row.Email)
}

func (s *Service) Resend(ctx context.Context, patientID uuid.UUID) (*InvitationResult, error) {
	org, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.Get(ctx, patientID)
	if err != nil {
		return nil, err
	}
	l := s.limiter()
	l.mu.Lock()
	now := time.Now()
	if last, ok := l.last[patientID]; ok && now.Sub(last) < time.Minute {
		l.mu.Unlock()
		return nil, ErrInviteRateLimited
	}
	if l.count[patientID] >= 5 && now.Sub(l.last[patientID]) < 24*time.Hour {
		l.mu.Unlock()
		return nil, ErrInviteRateLimited
	}
	l.last[patientID], l.count[patientID] = now, l.count[patientID]+1
	l.mu.Unlock()
	q := tenant.Queries(ctx, s.q)
	if err = q.RevokePendingInvitations(ctx, db.RevokePendingInvitationsParams{PatientID: patientID, OrganizationID: org}); err != nil {
		return nil, err
	}
	return s.createInvitation(ctx, *p, p.Email)
}

func (s *Service) Validate(ctx context.Context, raw string) (InvitationStatus, error) {
	h := sha256.Sum256([]byte(raw))
	row, err := s.q.GetInvitationByTokenHash(ctx, h[:])
	if err != nil {
		return InvitationStatus{}, ErrInviteNotFound
	}
	status := row.Status
	if status == "pending" && time.Now().After(row.ExpiresAt) {
		status = "expired"
	}
	return InvitationStatus{ID: row.ID, Status: status, DeliveryStatus: row.DeliveryStatus, ExpiresAt: row.ExpiresAt}, nil
}

func (s *Service) Accept(ctx context.Context, raw, password string) error {
	h := sha256.Sum256([]byte(raw))
	row, err := s.q.GetInvitationByTokenHash(ctx, h[:])
	if err != nil || row.Status != "pending" || time.Now().After(row.ExpiresAt) {
		return ErrInviteNotFound
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	userID, err := s.q.CreatePatientUser(ctx, db.CreatePatientUserParams{OrganizationID: &row.OrganizationID, Email: row.Email, PasswordHash: string(hash)})
	if err != nil {
		return err
	}
	if err = s.q.AttachPatientUser(ctx, db.AttachPatientUserParams{PatientID: row.PatientID, UserID: &userID}); err != nil {
		return err
	}
	if err = s.q.ActivatePatientRelationship(ctx, row.PatientID); err != nil {
		return err
	}
	return s.q.AcceptInvitation(ctx, row.ID)
}

func (s *Service) link(token string) string {
	return strings.TrimRight(s.frontendURL, "/") + "/invite/" + token
}
func (s *Service) mailBody(name, link string) string {
	return fmt.Sprintf("Olá, %s. Você recebeu um convite para acessar o Acolhe. O link é válido por 72 horas:\n\n%s\n\nSe não esperava este convite, ignore esta mensagem.", name, link)
}

func (s *Service) Delivery(ctx context.Context, id uuid.UUID) error {
	row, err := s.q.GetInvitationByID(ctx, id)
	if err != nil {
		return err
	}
	token, err := invitation.Decrypt(row.TokenCiphertext, s.tokenKey)
	if err != nil {
		return err
	}
	if err = s.mailer.Send(row.Email, "Seu convite para o Acolhe", s.mailBody("paciente", s.link(token))); err != nil {
		return err
	}
	return s.q.MarkInvitationSent(ctx, id)
}

func defaultMailer() EmailSender {
	return SMTPMailer{Host: os.Getenv("SMTP_HOST"), Port: envOr("SMTP_PORT", "587"), Username: os.Getenv("SMTP_USERNAME"), Password: os.Getenv("SMTP_PASSWORD"), From: envOr("SMTP_FROM", "no-reply@acolhe.local")}
}
func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
