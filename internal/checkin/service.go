// Package checkin cobre os check-ins de humor/estado do paciente entre sessões.
package checkin

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

var (
	// ErrPatientNotInOrg: paciente inexistente ou fora da organização.
	ErrPatientNotInOrg      = errors.New("paciente não encontrado na organização")
	ErrDailyCheckinExists   = errors.New("já existe um check-in para este paciente hoje")
	ErrPsychologistRequired = errors.New("ação restrita a psicólogos")
	ErrInvalidNote          = errors.New("a observação deve ter no máximo 1.000 caracteres")
	ErrInvalidMood          = errors.New("humor deve estar entre 1 e 5")
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
	Day       string    `json:"day"`
	UpdatedAt time.Time `json:"updatedAt"`
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
	if req.Note != nil {
		trimmed := strings.TrimSpace(*req.Note)
		if len([]rune(trimmed)) > 1000 {
			return nil, ErrInvalidNote
		}
		if trimmed == "" {
			req.Note = nil
		} else {
			req.Note = &trimmed
		}
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

	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	if _, err := q.GetActiveRelationship(ctx, db.GetActiveRelationshipParams{
		PatientID: req.PatientID, PsychologistID: psy.ID, OrganizationID: orgID,
	}); err != nil {
		return nil, ErrPatientNotInOrg
	}
	row, err := q.CreateCheckin(ctx, db.CreateCheckinParams{
		PatientID: req.PatientID,
		Mood:      req.Mood,
		Note:      req.Note,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDailyCheckinExists
	}
	if err != nil {
		return nil, err
	}
	return &Checkin{ID: row.ID, PatientID: row.PatientID, Mood: row.Mood, Note: row.Note, CreatedAt: row.CreatedAt, Day: row.Day, UpdatedAt: row.UpdatedAt}, nil
}

// List devolve os check-ins de um paciente, isolados por organização.
func (s *Service) List(ctx context.Context, patientID uuid.UUID) ([]Checkin, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	rows, err := tenant.Queries(ctx, s.q).ListCheckinsByPatient(ctx, db.ListCheckinsByPatientParams{
		PatientID:      patientID,
		OrganizationID: orgID,
		PsychologistID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Checkin, 0, len(rows))
	for _, r := range rows {
		out = append(out, Checkin{ID: r.ID, PatientID: r.PatientID, Mood: r.Mood, Note: r.Note, CreatedAt: r.CreatedAt, Day: r.Day, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}
