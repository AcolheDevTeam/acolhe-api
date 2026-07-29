package document_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/document"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

func TestGeneratePDFValidatesRoleInputAndQueue(t *testing.T) {
	request := document.GenerateRequest{PatientID: uuid.New(), Type: "declaration"}
	patientContext := tenant.WithIdentity(context.Background(), tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "patient",
	})
	_, err := document.NewService(&testsupport.FakeQuerier{}, &testsupport.FakeEnqueuer{}).
		GeneratePDF(patientContext, request)
	assert.ErrorIs(t, err, document.ErrPsychologistRequired)

	psychologistContext := tenant.WithIdentity(context.Background(), tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	})
	_, err = document.NewService(&testsupport.FakeQuerier{}, &testsupport.FakeEnqueuer{}).
		GeneratePDF(psychologistContext, document.GenerateRequest{Type: "declaration"})
	assert.ErrorIs(t, err, document.ErrInvalidInput)
	_, err = document.NewService(&testsupport.FakeQuerier{}, &testsupport.FakeEnqueuer{}).
		GeneratePDF(psychologistContext, document.GenerateRequest{PatientID: uuid.New(), Type: " "})
	assert.ErrorIs(t, err, document.ErrInvalidInput)
	_, err = document.NewService(&testsupport.FakeQuerier{}, nil).
		GeneratePDF(psychologistContext, request)
	assert.ErrorIs(t, err, document.ErrQueueUnavailable)
}

func TestGeneratePDFEnforcesPatientOwnershipAndEnqueues(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	patientID, psychologistID, documentID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(_ context.Context, userID uuid.UUID) (db.GetPsychologistByUserRow, error) {
			assert.Equal(t, identity.UserID, userID)
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
		CreateDocumentFn: func(_ context.Context, arg db.CreateDocumentParams) (db.CreateDocumentRow, error) {
			assert.Equal(t, "declaration", arg.Type)
			return db.CreateDocumentRow{
				ID: documentID, PatientID: arg.PatientID,
				PsychologistID: arg.PsychologistID, Type: arg.Type, CreatedAt: now,
			}, nil
		},
	}
	queue := &testsupport.FakeEnqueuer{}
	created, err := document.NewService(fake, queue).GeneratePDF(
		tenant.WithIdentity(context.Background(), identity),
		document.GenerateRequest{PatientID: patientID, Type: " declaration "},
	)
	require.NoError(t, err)
	assert.Equal(t, documentID, created.ID)
	require.NotNil(t, queue.Task)
	assert.Equal(t, tasks.TypePDF, queue.Task.Type())
	var payload tasks.PDFPayload
	require.NoError(t, json.Unmarshal(queue.Task.Payload(), &payload))
	assert.Equal(t, documentID, payload.DocumentID)
	assert.Equal(t, patientID, payload.PatientID)
	assert.Equal(t, psychologistID, payload.PsychologistID)

	queue.Err = errors.New("Redis down")
	_, err = document.NewService(fake, queue).GeneratePDF(
		tenant.WithIdentity(context.Background(), identity),
		document.GenerateRequest{PatientID: patientID, Type: "declaration"},
	)
	assert.ErrorContains(t, err, "Redis")
}

func TestListDocumentsCarriesOrganizationPredicate(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	patientID := uuid.New()
	fake := &testsupport.FakeQuerier{
		ListDocumentsFn: func(
			_ context.Context,
			arg db.ListDocumentsByPatientParams,
		) ([]db.ListDocumentsByPatientRow, error) {
			assert.Equal(t, patientID, arg.PatientID)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return nil, nil
		},
	}
	documents, err := document.NewService(fake, &testsupport.FakeEnqueuer{}).List(
		tenant.WithIdentity(context.Background(), identity),
		patientID,
	)
	require.NoError(t, err)
	assert.Empty(t, documents)
}
