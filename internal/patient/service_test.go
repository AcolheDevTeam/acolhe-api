package patient_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/patient"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

type fakeEnqueuer struct {
	task *asynq.Task
	err  error
}

func (queue *fakeEnqueuer) EnqueueContext(
	_ context.Context,
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	queue.task = task
	if queue.err != nil {
		return nil, queue.err
	}
	return &asynq.TaskInfo{ID: uuid.NewString(), Type: task.Type()}, nil
}

func TestRequestExportPersistsSLAAndEnqueuesIdentity(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "patient",
	}
	patientID := uuid.New()
	var created db.CreateLGPDExportRequestParams
	fake := &testsupport.FakeQuerier{
		GetPatientExportAccessFn: func(
			_ context.Context,
			arg db.GetPatientExportAccessParams,
		) (db.GetPatientExportAccessRow, error) {
			assert.Equal(t, patientID, arg.PatientID)
			assert.Equal(t, identity.UserID, arg.RequestedBy)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return db.GetPatientExportAccessRow{
				ID: patientID, OrganizationID: identity.OrgID,
				RequesterEmail: "paciente@example.test",
			}, nil
		},
		CreateLGPDExportRequestFn: func(
			_ context.Context,
			arg db.CreateLGPDExportRequestParams,
		) (db.LgpdExportRequest, error) {
			created = arg
			return db.LgpdExportRequest{ID: arg.ID}, nil
		},
	}
	queue := &fakeEnqueuer{}
	service := patient.NewService(fake, queue)
	ctx := tenant.WithIdentity(context.Background(), identity)

	require.NoError(t, service.RequestExport(ctx, patientID))
	require.NotNil(t, queue.task)
	assert.Equal(t, tasks.TypeLGPDExport, queue.task.Type())
	assert.WithinDuration(t, created.RequestedAt.Add(24*time.Hour), created.SlaDeadline, 0)
	assert.Equal(t, patientID, created.PatientID)
	assert.Equal(t, identity.OrgID, created.OrganizationID)
	assert.Equal(t, identity.UserID, created.RequestedBy)

	var payload tasks.LGPDExportPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	assert.Equal(t, created.ID, payload.RequestID)
	assert.Equal(t, patientID, payload.PatientID)
	assert.Equal(t, identity.OrgID, payload.OrganizationID)
	assert.Equal(t, identity.UserID, payload.RequestedBy)
	assert.Equal(t, "patient", payload.RequesterRole)
	assert.Equal(t, created.RequestedAt, payload.RequestedAt)
}

func TestRequestExportRecordsQueueFailure(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	patientID := uuid.New()
	var failed db.MarkLGPDExportQueueFailedParams
	fake := &testsupport.FakeQuerier{
		GetPatientExportAccessFn: func(
			context.Context,
			db.GetPatientExportAccessParams,
		) (db.GetPatientExportAccessRow, error) {
			return db.GetPatientExportAccessRow{
				ID: patientID, OrganizationID: identity.OrgID,
			}, nil
		},
		CreateLGPDExportRequestFn: func(
			_ context.Context,
			arg db.CreateLGPDExportRequestParams,
		) (db.LgpdExportRequest, error) {
			return db.LgpdExportRequest{ID: arg.ID}, nil
		},
		MarkLGPDExportQueueFailedFn: func(
			_ context.Context,
			arg db.MarkLGPDExportQueueFailedParams,
		) (int64, error) {
			failed = arg
			return 1, nil
		},
	}
	queue := &fakeEnqueuer{err: errors.New("Redis indisponível")}
	service := patient.NewService(fake, queue)

	err := service.RequestExport(
		tenant.WithIdentity(context.Background(), identity),
		patientID,
	)
	require.ErrorContains(t, err, "Redis")
	assert.NotEqual(t, uuid.Nil, failed.ID)
	require.NotNil(t, failed.LastError)
	assert.Contains(t, *failed.LastError, "Redis")
}

func TestRequestExportHidesUnauthorizedPatient(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "patient",
	}
	queue := &fakeEnqueuer{}
	fake := &testsupport.FakeQuerier{
		GetPatientExportAccessFn: func(
			context.Context,
			db.GetPatientExportAccessParams,
		) (db.GetPatientExportAccessRow, error) {
			return db.GetPatientExportAccessRow{}, pgx.ErrNoRows
		},
	}
	err := patient.NewService(fake, queue).RequestExport(
		tenant.WithIdentity(context.Background(), identity),
		uuid.New(),
	)
	assert.ErrorIs(t, err, patient.ErrNotFound)
	assert.Nil(t, queue.task)
}
