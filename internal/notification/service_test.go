package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/notification"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

func TestEnqueueReminderValidatesInputAndQueue(t *testing.T) {
	valid := notification.ReminderRequest{
		AppointmentID: uuid.New(),
		UserID:        uuid.New(),
		ScheduledFor:  time.Now().UTC().Add(time.Hour),
	}

	tests := []struct {
		name    string
		request notification.ReminderRequest
	}{
		{name: "missing appointment", request: notification.ReminderRequest{
			UserID: valid.UserID, ScheduledFor: valid.ScheduledFor,
		}},
		{name: "missing user", request: notification.ReminderRequest{
			AppointmentID: valid.AppointmentID, ScheduledFor: valid.ScheduledFor,
		}},
		{name: "missing schedule", request: notification.ReminderRequest{
			AppointmentID: valid.AppointmentID, UserID: valid.UserID,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue := &testsupport.FakeEnqueuer{}
			err := notification.NewService(queue).EnqueueReminder(context.Background(), test.request)
			assert.ErrorIs(t, err, notification.ErrInvalidInput)
			assert.Zero(t, queue.Calls)
		})
	}

	err := notification.NewService(nil).EnqueueReminder(context.Background(), valid)
	assert.ErrorIs(t, err, notification.ErrQueueUnavailable)
}

func TestEnqueueReminderProducesContractPayload(t *testing.T) {
	request := notification.ReminderRequest{
		AppointmentID: uuid.New(),
		UserID:        uuid.New(),
		ScheduledFor:  time.Now().UTC().Add(30 * time.Minute).Truncate(time.Millisecond),
	}
	queue := &testsupport.FakeEnqueuer{}

	err := notification.NewService(queue).EnqueueReminder(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, queue.Task)
	assert.Equal(t, 1, queue.Calls)
	assert.Equal(t, tasks.TypeReminder, queue.Task.Type())

	var payload tasks.ReminderPayload
	require.NoError(t, json.Unmarshal(queue.Task.Payload(), &payload))
	assert.Equal(t, request.AppointmentID, payload.AppointmentID)
	assert.Equal(t, request.UserID, payload.UserID)
	assert.Equal(t, request.ScheduledFor, payload.ScheduledFor)
}

func TestEnqueueReminderPropagatesQueueFailure(t *testing.T) {
	queueErr := errors.New("Redis unavailable")
	queue := &testsupport.FakeEnqueuer{Err: queueErr}
	err := notification.NewService(queue).EnqueueReminder(context.Background(), notification.ReminderRequest{
		AppointmentID: uuid.New(),
		UserID:        uuid.New(),
		ScheduledFor:  time.Now().UTC().Add(time.Hour),
	})

	assert.ErrorIs(t, err, queueErr)
	assert.Equal(t, 1, queue.Calls)
}
