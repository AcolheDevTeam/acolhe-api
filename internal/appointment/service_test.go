package appointment_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/appointment"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

func appointmentIdentity() (context.Context, tenant.Identity, uuid.UUID) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	return tenant.WithIdentity(context.Background(), identity), identity, uuid.New()
}

func TestCreateAppointmentRejectsInvalidInput(t *testing.T) {
	ctx, _, _ := appointmentIdentity()
	valid := appointment.CreateRequest{
		PatientID: uuid.New(), ScheduledFor: time.Now().UTC(),
		DurationMinutes: 50, Modality: "online",
	}
	tests := []struct {
		name string
		edit func(*appointment.CreateRequest)
	}{
		{name: "missing patient", edit: func(request *appointment.CreateRequest) { request.PatientID = uuid.Nil }},
		{name: "missing date", edit: func(request *appointment.CreateRequest) { request.ScheduledFor = time.Time{} }},
		{name: "negative duration", edit: func(request *appointment.CreateRequest) { request.DurationMinutes = -1 }},
		{name: "too short", edit: func(request *appointment.CreateRequest) { request.DurationMinutes = 10 }},
		{name: "too long", edit: func(request *appointment.CreateRequest) { request.DurationMinutes = 481 }},
		{name: "invalid modality", edit: func(request *appointment.CreateRequest) { request.Modality = "phone" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.edit(&request)
			_, err := appointment.NewService(&testsupport.FakeQuerier{}).Create(ctx, request)
			assert.ErrorIs(t, err, appointment.ErrInvalidInput)
		})
	}
}

func TestCreateAppointmentDetectsOverlapBeforeInsert(t *testing.T) {
	ctx, identity, psychologistID := appointmentIdentity()
	scheduledFor := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(_ context.Context, userID uuid.UUID) (db.GetPsychologistByUserRow, error) {
			assert.Equal(t, identity.UserID, userID)
			return db.GetPsychologistByUserRow{ID: psychologistID}, nil
		},
		GetPatientForPsychFn: func(
			_ context.Context,
			arg db.GetPatientForPsychologistParams,
		) (db.GetPatientForPsychologistRow, error) {
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			assert.Equal(t, psychologistID, arg.PsychologistID)
			return db.GetPatientForPsychologistRow{ID: arg.ID}, nil
		},
		CountAppointmentConflFn: func(
			_ context.Context,
			arg db.CountAppointmentConflictsParams,
		) (int64, error) {
			assert.Equal(t, psychologistID, arg.PsychologistID)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			assert.Equal(t, scheduledFor, arg.WindowStart)
			assert.Equal(t, scheduledFor.Add(50*time.Minute), arg.WindowEnd)
			return 1, nil
		},
		CreateAppointmentFn: func(context.Context, db.CreateAppointmentParams) (db.CreateAppointmentRow, error) {
			t.Fatal("conflicting appointment must not be inserted")
			return db.CreateAppointmentRow{}, nil
		},
	}
	_, err := appointment.NewService(fake).Create(ctx, appointment.CreateRequest{
		PatientID: uuid.New(), ScheduledFor: scheduledFor,
	})
	assert.ErrorIs(t, err, appointment.ErrScheduleConflict)
}

func TestCreateAppointmentAppliesDefaults(t *testing.T) {
	ctx, identity, psychologistID := appointmentIdentity()
	patientID, appointmentID := uuid.New(), uuid.New()
	scheduledFor := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psychologistID}, nil
		},
		GetPatientForPsychFn: func(
			_ context.Context,
			arg db.GetPatientForPsychologistParams,
		) (db.GetPatientForPsychologistRow, error) {
			assert.Equal(t, patientID, arg.ID)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			assert.Equal(t, psychologistID, arg.PsychologistID)
			return db.GetPatientForPsychologistRow{ID: patientID}, nil
		},
		CountAppointmentConflFn: func(context.Context, db.CountAppointmentConflictsParams) (int64, error) {
			return 0, nil
		},
		CreateAppointmentFn: func(_ context.Context, arg db.CreateAppointmentParams) (db.CreateAppointmentRow, error) {
			assert.Equal(t, int32(50), arg.DurationMinutes)
			assert.Equal(t, "in_person", arg.Modality)
			return db.CreateAppointmentRow{
				ID: appointmentID, PatientID: arg.PatientID,
				PsychologistID: arg.PsychologistID, ScheduledFor: arg.ScheduledFor,
				DurationMinutes: arg.DurationMinutes, Modality: arg.Modality,
				Status: "scheduled", CreatedAt: time.Now().UTC(),
			}, nil
		},
	}
	created, err := appointment.NewService(fake).Create(ctx, appointment.CreateRequest{
		PatientID: patientID, ScheduledFor: scheduledFor,
	})
	require.NoError(t, err)
	assert.Equal(t, appointmentID, created.ID)
	assert.Equal(t, "scheduled", created.Status)
}

func TestCreateAppointmentHidesPatientsOutsideRelationship(t *testing.T) {
	ctx, _, psychologistID := appointmentIdentity()
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psychologistID}, nil
		},
		GetPatientForPsychFn: func(
			context.Context,
			db.GetPatientForPsychologistParams,
		) (db.GetPatientForPsychologistRow, error) {
			return db.GetPatientForPsychologistRow{}, pgx.ErrNoRows
		},
		CountAppointmentConflFn: func(
			context.Context,
			db.CountAppointmentConflictsParams,
		) (int64, error) {
			t.Fatal("must reject access before reading the calendar")
			return 0, nil
		},
	}
	_, err := appointment.NewService(fake).Create(ctx, appointment.CreateRequest{
		PatientID: uuid.New(), ScheduledFor: time.Now().UTC().Add(time.Hour),
	})
	assert.ErrorIs(t, err, appointment.ErrNotFound)
}

func TestAppointmentStatusTransitionsAreOwnedAndIdempotent(t *testing.T) {
	ctx, identity, psychologistID := appointmentIdentity()
	appointmentID, patientID := uuid.New(), uuid.New()
	currentStatus := "scheduled"
	updates := 0
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psychologistID}, nil
		},
		GetAppointmentForPsychFn: func(
			_ context.Context,
			arg db.GetAppointmentForPsychologistParams,
		) (db.GetAppointmentForPsychologistRow, error) {
			assert.Equal(t, appointmentID, arg.ID)
			assert.Equal(t, psychologistID, arg.PsychologistID)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return db.GetAppointmentForPsychologistRow{
				ID: appointmentID, PatientID: patientID, PsychologistID: psychologistID,
				ScheduledFor: time.Now().UTC(), DurationMinutes: 50,
				Modality: "online", Status: currentStatus, CreatedAt: time.Now().UTC(),
			}, nil
		},
		UpdateAppointmentStatusFn: func(
			_ context.Context,
			arg db.UpdateAppointmentStatusParams,
		) (db.UpdateAppointmentStatusRow, error) {
			updates++
			assert.Equal(t, currentStatus, arg.CurrentStatus)
			currentStatus = arg.Status
			return db.UpdateAppointmentStatusRow{
				ID: appointmentID, PatientID: patientID, PsychologistID: psychologistID,
				ScheduledFor: time.Now().UTC(), DurationMinutes: 50,
				Modality: "online", Status: arg.Status, CreatedAt: time.Now().UTC(),
			}, nil
		},
	}
	service := appointment.NewService(fake)
	confirmed, err := service.TransitionStatus(ctx, appointmentID, "confirmed")
	require.NoError(t, err)
	assert.Equal(t, "confirmed", confirmed.Status)
	replay, err := service.TransitionStatus(ctx, appointmentID, "confirmed")
	require.NoError(t, err)
	assert.Equal(t, "confirmed", replay.Status)
	assert.Equal(t, 1, updates)
	completed, err := service.TransitionStatus(ctx, appointmentID, "completed")
	require.NoError(t, err)
	assert.Equal(t, "completed", completed.Status)
	_, err = service.TransitionStatus(ctx, appointmentID, "scheduled")
	assert.ErrorIs(t, err, appointment.ErrInvalidStatusTransition)

	fake.GetAppointmentForPsychFn = func(
		context.Context,
		db.GetAppointmentForPsychologistParams,
	) (db.GetAppointmentForPsychologistRow, error) {
		return db.GetAppointmentForPsychologistRow{}, pgx.ErrNoRows
	}
	_, err = service.TransitionStatus(ctx, appointmentID, "canceled")
	assert.ErrorIs(t, err, appointment.ErrNotFound)
}
