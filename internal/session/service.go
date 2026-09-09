// Package session cobre as sessões clínicas (prontuário) e a timeline do paciente.
// Concentra a regra clínica de vínculo ativo psicólogo↔paciente (spec §4.4).
package session

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

type Service struct {
	q db.Querier
}

func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

// Create registra uma sessão clínica. Exige vínculo ativo entre o psicólogo
// autenticado e o paciente — sem vínculo, devolve ErrNoActiveRelationship.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Session, error) {
	req.Notes = strings.TrimSpace(req.Notes)
	if req.PatientID == uuid.Nil || req.OccurredAt.IsZero() || req.OccurredAt.After(time.Now().Add(5*time.Minute)) ||
		req.OccurredAt.Before(time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)) ||
		len(req.Notes) == 0 || len(req.Notes) > 10000 {
		return nil, ErrInvalidInput
	}
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}

	// O psicólogo é o usuário autenticado; resolvemos seu perfil clínico.
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}

	// Regra: vínculo ativo obrigatório (com isolamento de org via patient_profile).
	rel, err := tenant.Queries(ctx, s.q).GetActiveRelationship(ctx, db.GetActiveRelationshipParams{
		PatientID:      req.PatientID,
		PsychologistID: psy.ID,
		OrganizationID: id.OrgID,
	})
	if err != nil || rel.Status != "active" {
		return nil, ErrNoActiveRelationship
	}

	row, err := tenant.Queries(ctx, s.q).CreateSession(ctx, db.CreateSessionParams{
		PatientID:      req.PatientID,
		PsychologistID: psy.ID,
		OccurredAt:     req.OccurredAt,
		Status:         "pending",
	})
	if err != nil {
		return nil, err
	}
	if err := tenant.Queries(ctx, s.q).CreateClinicalRecord(ctx, db.CreateClinicalRecordParams{
		SessionID: row.ID, PatientID: row.PatientID, PsychologistID: row.PsychologistID, Notes: req.Notes,
	}); err != nil {
		return nil, err
	}
	return &Session{
		ID:             row.ID,
		PatientID:      row.PatientID,
		PsychologistID: row.PsychologistID,
		OccurredAt:     row.OccurredAt,
		Status:         row.Status,
		Notes:          req.Notes,
		CreatedAt:      row.CreatedAt,
	}, nil
}

// List devolve as sessões de um paciente, isoladas pela organização.
func (s *Service) List(ctx context.Context, patientID uuid.UUID) ([]Session, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	rows, err := tenant.Queries(ctx, s.q).GetSessionsByPatient(ctx, db.GetSessionsByPatientParams{
		PatientID:      patientID,
		PsychologistID: psy.ID,
		OrganizationID: id.OrgID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session{
			ID:             r.ID,
			PatientID:      r.PatientID,
			PsychologistID: r.PsychologistID,
			OccurredAt:     r.OccurredAt,
			Status:         r.Status,
			Modality:       r.Modality,
			DurationMin:    r.DurationMinutes,
			CreatedAt:      r.CreatedAt,
		})
	}
	return out, nil
}

// ListAll devolve todas as sessões da organização do requisitante.
func (s *Service) ListAll(ctx context.Context) ([]Session, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	rows, err := tenant.Queries(ctx, s.q).GetSessionsByPsychologist(ctx, db.GetSessionsByPsychologistParams{
		OrganizationID: id.OrgID, PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session{
			ID: r.ID, PatientID: r.PatientID, PsychologistID: r.PsychologistID,
			PatientName: r.PatientName, OccurredAt: r.OccurredAt, Status: r.Status,
			Modality: r.Modality, DurationMin: r.DurationMinutes, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// Get devolve uma sessão da organização do requisitante.
func (s *Service) Get(ctx context.Context, sessionID uuid.UUID) (*Session, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	r, err := tenant.Queries(ctx, s.q).GetSession(ctx, db.GetSessionParams{
		ID: sessionID, PsychologistID: psy.ID, OrganizationID: id.OrgID,
	})
	if err != nil {
		return nil, ErrNotFound
	}
	return &Session{
		ID: r.ID, PatientID: r.PatientID, PsychologistID: r.PsychologistID,
		PatientName: r.PatientName, OccurredAt: r.OccurredAt, Status: r.Status,
		Notes: r.Notes, Modality: r.Modality, DurationMin: r.DurationMinutes, CreatedAt: r.CreatedAt,
	}, nil
}

// Timeline devolve a linha do tempo unificada (sessões + agenda + atividades).
func (s *Service) Timeline(ctx context.Context, patientID uuid.UUID) ([]TimelineItem, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	if _, err := q.GetActiveRelationship(ctx, db.GetActiveRelationshipParams{
		PatientID: patientID, PsychologistID: psy.ID, OrganizationID: id.OrgID,
	}); err != nil {
		return nil, ErrNoActiveRelationship
	}
	rows, err := q.GetPatientTimeline(ctx, db.GetPatientTimelineParams{
		PatientID:      patientID,
		OrganizationID: id.OrgID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]TimelineItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, TimelineItem{Kind: r.Kind, ItemID: r.ItemID, OccurredAt: r.OccurredAt, Status: r.Status})
	}
	return out, nil
}
