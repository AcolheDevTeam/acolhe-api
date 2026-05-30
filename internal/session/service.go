// Package session cobre as sessões clínicas (prontuário) e a timeline do paciente.
// Concentra a regra clínica de vínculo ativo psicólogo↔paciente (spec §4.4).
package session

import (
	"context"

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
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, tenant.ErrNoTenant
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
	return &Session{
		ID:             row.ID,
		PatientID:      row.PatientID,
		PsychologistID: row.PsychologistID,
		OccurredAt:     row.OccurredAt,
		Status:         row.Status,
		CreatedAt:      row.CreatedAt,
	}, nil
}

// List devolve as sessões de um paciente, isoladas pela organização.
func (s *Service) List(ctx context.Context, patientID uuid.UUID) ([]Session, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tenant.Queries(ctx, s.q).GetSessionsByPatient(ctx, db.GetSessionsByPatientParams{
		PatientID:      patientID,
		OrganizationID: orgID,
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
			CreatedAt:      r.CreatedAt,
		})
	}
	return out, nil
}

// Timeline devolve a linha do tempo unificada (sessões + agenda + atividades).
func (s *Service) Timeline(ctx context.Context, patientID uuid.UUID) ([]TimelineItem, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tenant.Queries(ctx, s.q).GetPatientTimeline(ctx, db.GetPatientTimelineParams{
		PatientID:      patientID,
		OrganizationID: orgID,
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
