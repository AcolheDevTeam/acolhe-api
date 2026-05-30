// Package checkin cobre os check-ins de humor/estado do paciente entre sessões.
package checkin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

var (
	// ErrPatientNotInOrg: paciente inexistente ou fora da organização.
	ErrPatientNotInOrg = errors.New("paciente não encontrado na organização")
	// ErrInvalidMood: humor fora do intervalo 1..5.
	ErrInvalidMood = errors.New("humor deve estar entre 1 e 5")
)

type Service struct {
	q db.Querier
}

func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

// CreateRequest é o corpo de POST /checkins.
type CreateRequest struct {
	PatientID uuid.UUID `json:"patientId"`
	Mood      int32     `json:"mood"`
	Note      *string   `json:"note"`
}

// Checkin é a projeção pública de um check-in.
type Checkin struct {
	ID        uuid.UUID `json:"id"`
	PatientID uuid.UUID `json:"patientId"`
	Mood      int32     `json:"mood"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
}

// Create registra um check-in, validando o humor e o vínculo do paciente à org.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Checkin, error) {
	if req.Mood < 1 || req.Mood > 5 {
		return nil, ErrInvalidMood
	}
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	q := tenant.Queries(ctx, s.q)

	inOrg, err := q.PatientInOrg(ctx, db.PatientInOrgParams{PatientID: req.PatientID, OrganizationID: orgID})
	if err != nil {
		return nil, err
	}
	if !inOrg {
		return nil, ErrPatientNotInOrg
	}

	row, err := q.CreateCheckin(ctx, db.CreateCheckinParams{
		PatientID: req.PatientID,
		Mood:      req.Mood,
		Note:      req.Note,
	})
	if err != nil {
		return nil, err
	}
	return &Checkin{ID: row.ID, PatientID: row.PatientID, Mood: row.Mood, Note: row.Note, CreatedAt: row.CreatedAt}, nil
}

// List devolve os check-ins de um paciente, isolados por organização.
func (s *Service) List(ctx context.Context, patientID uuid.UUID) ([]Checkin, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tenant.Queries(ctx, s.q).ListCheckinsByPatient(ctx, db.ListCheckinsByPatientParams{
		PatientID:      patientID,
		OrganizationID: orgID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Checkin, 0, len(rows))
	for _, r := range rows {
		out = append(out, Checkin{ID: r.ID, PatientID: r.PatientID, Mood: r.Mood, Note: r.Note, CreatedAt: r.CreatedAt})
	}
	return out, nil
}
