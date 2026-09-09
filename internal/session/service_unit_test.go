package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/session"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

func TestCreateSessionRejectsInvalidClinicalInput(t *testing.T) {
	valid := session.CreateRequest{
		PatientID: uuid.New(), OccurredAt: time.Now().UTC(), Notes: "Registro clínico",
	}
	tests := []struct {
		name string
		edit func(*session.CreateRequest)
	}{
		{name: "missing patient", edit: func(request *session.CreateRequest) { request.PatientID = uuid.Nil }},
		{name: "missing date", edit: func(request *session.CreateRequest) { request.OccurredAt = time.Time{} }},
		{name: "future date", edit: func(request *session.CreateRequest) { request.OccurredAt = time.Now().Add(time.Hour) }},
		{name: "empty notes", edit: func(request *session.CreateRequest) { request.Notes = "  " }},
		{name: "oversized notes", edit: func(request *session.CreateRequest) { request.Notes = strings.Repeat("a", 10001) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.edit(&request)
			_, err := session.NewService(&testsupport.FakeQuerier{}).Create(
				context.Background(),
				request,
			)
			assert.ErrorIs(t, err, session.ErrInvalidInput)
		})
	}
}

func TestCreateSessionRequiresPsychologistAndActiveRelationship(t *testing.T) {
	request := session.CreateRequest{
		PatientID: uuid.New(), OccurredAt: time.Now().UTC(), Notes: "Registro",
	}
	patientContext := tenant.WithIdentity(context.Background(), tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "patient",
	})
	_, err := session.NewService(&testsupport.FakeQuerier{}).Create(patientContext, request)
	assert.ErrorIs(t, err, session.ErrPsychologistRequired)

	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	psychologistID := uuid.New()
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psychologistID}, nil
		},
		GetActiveRelationshipFn: func(
			_ context.Context,
			arg db.GetActiveRelationshipParams,
		) (db.GetActiveRelationshipRow, error) {
			assert.Equal(t, request.PatientID, arg.PatientID)
			assert.Equal(t, psychologistID, arg.PsychologistID)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return db.GetActiveRelationshipRow{}, pgx.ErrNoRows
		},
	}
	_, err = session.NewService(fake).Create(
		tenant.WithIdentity(context.Background(), identity),
		request,
	)
	assert.ErrorIs(t, err, session.ErrNoActiveRelationship)
}

func TestCreateSessionWritesRecordThroughSameTenantQuerier(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	patientID, psychologistID, sessionID := uuid.New(), uuid.New(), uuid.New()
	occurredAt := time.Now().UTC().Add(-time.Hour)
	recorded := false
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(_ context.Context, userID uuid.UUID) (db.GetPsychologistByUserRow, error) {
			assert.Equal(t, identity.UserID, userID)
			return db.GetPsychologistByUserRow{ID: psychologistID}, nil
		},
		GetActiveRelationshipFn: func(
			_ context.Context,
			arg db.GetActiveRelationshipParams,
		) (db.GetActiveRelationshipRow, error) {
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return db.GetActiveRelationshipRow{Status: "active"}, nil
		},
		CreateSessionFn: func(_ context.Context, arg db.CreateSessionParams) (db.CreateSessionRow, error) {
			assert.Equal(t, patientID, arg.PatientID)
			assert.Equal(t, psychologistID, arg.PsychologistID)
			assert.Equal(t, "pending", arg.Status)
			return db.CreateSessionRow{
				ID: sessionID, PatientID: arg.PatientID, PsychologistID: arg.PsychologistID,
				OccurredAt: arg.OccurredAt, Status: arg.Status, CreatedAt: occurredAt,
			}, nil
		},
		CreateClinicalRecordFn: func(_ context.Context, arg db.CreateClinicalRecordParams) error {
			recorded = true
			assert.Equal(t, sessionID, arg.SessionID)
			assert.Equal(t, "Notas sem espaços externos", arg.Notes)
			return nil
		},
	}
	created, err := session.NewService(fake).Create(
		tenant.WithIdentity(context.Background(), identity),
		session.CreateRequest{
			PatientID: patientID, OccurredAt: occurredAt,
			Notes: "  Notas sem espaços externos  ",
		},
	)
	require.NoError(t, err)
	assert.True(t, recorded)
	assert.Equal(t, sessionID, created.ID)

	expected := errors.New("record persistence failed")
	fake.CreateClinicalRecordFn = func(context.Context, db.CreateClinicalRecordParams) error {
		return expected
	}
	_, err = session.NewService(fake).Create(
		tenant.WithIdentity(context.Background(), identity),
		session.CreateRequest{
			PatientID: patientID, OccurredAt: occurredAt, Notes: "Notas",
		},
	)
	assert.ErrorIs(t, err, expected)
}
