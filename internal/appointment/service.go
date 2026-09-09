// Package appointment cobre a agenda. Concentra a regra de conflito de horário:
// um psicólogo não pode ter dois agendamentos com janelas sobrepostas (spec §4.4).
package appointment

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
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	req.Modality = strings.TrimSpace(req.Modality)
	if req.PatientID == uuid.Nil || req.ScheduledFor.IsZero() ||
		req.ScheduledFor.Before(time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)) ||
		req.DurationMinutes < 0 || req.DurationMinutes > 480 ||
		(req.DurationMinutes > 0 && req.DurationMinutes < 15) ||
		(req.Modality != "" && req.Modality != "in_person" && req.Modality != "online") {
		return nil, ErrInvalidInput
	}
	psy, err := tenant.Queries(ctx, s.q).GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	if _, err := tenant.Queries(ctx, s.q).GetPatientForPsychologist(
		ctx,
		db.GetPatientForPsychologistParams{
			ID: req.PatientID, OrganizationID: id.OrgID, PsychologistID: psy.ID,
		},
	); err != nil {
		return nil, ErrNotFound
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
		OrganizationID: id.OrgID,
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

var allowedStatusTransitions = map[string]map[string]bool{
	"scheduled": {
		"confirmed": true, "completed": true, "canceled": true, "no_show": true,
	},
	"confirmed": {
		"completed": true, "canceled": true, "no_show": true,
	},
	"completed": {},
	"canceled":  {},
	"no_show":   {},
}

// TransitionStatus applies the appointment state machine with an optimistic
// current-status predicate. Replaying the same final state is idempotent.
func (s *Service) TransitionStatus(
	ctx context.Context,
	appointmentID uuid.UUID,
	nextStatus string,
) (*Appointment, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return nil, ErrPsychologistRequired
	}
	nextStatus = strings.TrimSpace(nextStatus)
	if appointmentID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	if _, known := allowedStatusTransitions[nextStatus]; !known {
		return nil, ErrInvalidStatusTransition
	}
	q := tenant.Queries(ctx, s.q)
	psychologist, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}
	current, err := q.GetAppointmentForPsychologist(ctx, db.GetAppointmentForPsychologistParams{
		ID: appointmentID, PsychologistID: psychologist.ID, OrganizationID: id.OrgID,
	})
	if err != nil {
		return nil, ErrNotFound
	}
	if current.Status == nextStatus {
		return appointmentFromDetail(current), nil
	}
	if !allowedStatusTransitions[current.Status][nextStatus] {
		return nil, ErrInvalidStatusTransition
	}
	updated, err := q.UpdateAppointmentStatus(ctx, db.UpdateAppointmentStatusParams{
		Status: nextStatus, ID: appointmentID, PsychologistID: psychologist.ID,
		OrganizationID: id.OrgID, CurrentStatus: current.Status,
	})
	if err != nil {
		latest, latestErr := q.GetAppointmentForPsychologist(
			ctx,
			db.GetAppointmentForPsychologistParams{
				ID: appointmentID, PsychologistID: psychologist.ID, OrganizationID: id.OrgID,
			},
		)
		if latestErr == nil && latest.Status == nextStatus {
			return appointmentFromDetail(latest), nil
		}
		return nil, ErrInvalidStatusTransition
	}
	return appointmentFromUpdated(updated), nil
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

func appointmentFromDetail(row db.GetAppointmentForPsychologistRow) *Appointment {
	return toAppointment(
		row.ID, row.PatientID, row.PsychologistID, row.ScheduledFor,
		row.DurationMinutes, row.Modality, row.Status, row.CreatedAt,
	)
}

func appointmentFromUpdated(row db.UpdateAppointmentStatusRow) *Appointment {
	return toAppointment(
		row.ID, row.PatientID, row.PsychologistID, row.ScheduledFor,
		row.DurationMinutes, row.Modality, row.Status, row.CreatedAt,
	)
}
