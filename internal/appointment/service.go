// Package appointment cobre a agenda. Concentra a regra de conflito de horário:
// um psicólogo não pode ter dois agendamentos com janelas sobrepostas (spec §4.4).
package appointment

import (
	"context"
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

// CreateRequest é o corpo de POST /appointments.
type CreateRequest struct {
	PatientID       uuid.UUID `json:"patientId"`
	ScheduledFor    time.Time `json:"scheduledFor"`
	DurationMinutes int32     `json:"durationMinutes"`
	Modality        string    `json:"modality"`
}

// Appointment é a projeção pública de um agendamento.
type Appointment struct {
	ID              uuid.UUID `json:"id"`
	PatientID       uuid.UUID `json:"patientId"`
	PsychologistID  uuid.UUID `json:"psychologistId"`
	ScheduledFor    time.Time `json:"scheduledFor"`
	DurationMinutes int32     `json:"durationMinutes"`
	Modality        string    `json:"modality"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
}

const defaultDurationMinutes = 50

// Create agenda uma sessão, recusando sobreposição na agenda do psicólogo.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Appointment, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, tenant.ErrNoTenant
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}

	duration := req.DurationMinutes
	if duration <= 0 {
		duration = defaultDurationMinutes
	}
	windowStart := req.ScheduledFor
	windowEnd := req.ScheduledFor.Add(time.Duration(duration) * time.Minute)

	// Regra: nenhuma janela sobreposta na agenda deste psicólogo.
	conflicts, err := tenant.Queries(ctx, s.q).CountAppointmentConflicts(ctx, db.CountAppointmentConflictsParams{
		PsychologistID: psy.ID,
		WindowStart:    windowStart,
		WindowEnd:      windowEnd,
	})
	if err != nil {
		return nil, err
	}
	if conflicts > 0 {
		return nil, ErrScheduleConflict
	}

	modality := req.Modality
	if modality == "" {
		modality = "in_person"
	}
	row, err := tenant.Queries(ctx, s.q).CreateAppointment(ctx, db.CreateAppointmentParams{
		PatientID:       req.PatientID,
		PsychologistID:  psy.ID,
		ScheduledFor:    req.ScheduledFor,
		DurationMinutes: duration,
		Modality:        modality,
	})
	if err != nil {
		return nil, err
	}
	return toAppointment(row.ID, row.PatientID, row.PsychologistID, row.ScheduledFor,
		row.DurationMinutes, row.Modality, row.Status, row.CreatedAt), nil
}

// List devolve a agenda do psicólogo autenticado.
func (s *Service) List(ctx context.Context) ([]Appointment, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, tenant.ErrNoTenant
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	rows, err := tenant.Queries(ctx, s.q).ListAppointmentsByPsychologist(ctx, db.ListAppointmentsByPsychologistParams{
		PsychologistID: psy.ID,
		OrganizationID: id.OrgID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Appointment, 0, len(rows))
	for _, r := range rows {
		out = append(out, *toAppointment(r.ID, r.PatientID, r.PsychologistID, r.ScheduledFor,
			r.DurationMinutes, r.Modality, r.Status, r.CreatedAt))
	}
	return out, nil
}

func toAppointment(id, patientID, psyID uuid.UUID, scheduledFor time.Time,
	duration int32, modality, status string, createdAt time.Time) *Appointment {
	return &Appointment{
		ID:              id,
		PatientID:       patientID,
		PsychologistID:  psyID,
		ScheduledFor:    scheduledFor,
		DurationMinutes: duration,
		Modality:        modality,
		Status:          status,
		CreatedAt:       createdAt,
	}
}
