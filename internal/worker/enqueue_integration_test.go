//go:build integration

// Round-trip real no Redis (asynq): enfileira uma tarefa e confirma via Inspector.
//
//	go test -tags=integration ./internal/worker/...
//
// Requer Redis acessível (docker compose up / colima). Usa REDIS_ADDR ou o default.
package worker_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/tasks"
)

func redisAddr() string {
	if a := os.Getenv("REDIS_ADDR"); a != "" {
		return a
	}
	return "127.0.0.1:6379"
}

func TestEnqueueReminder_RoundTrip(t *testing.T) {
	opt := asynq.RedisClientOpt{Addr: redisAddr()}

	insp := asynq.NewInspector(opt)
	defer insp.Close()
	// Estado limpo antes do teste.
	_, _ = insp.DeleteAllPendingTasks("default")

	client := asynq.NewClient(opt)
	defer client.Close()

	task, err := tasks.NewPDFTask(tasks.PDFPayload{
		DocumentID: uuid.New(), PatientID: uuid.New(), PsychologistID: uuid.New(),
	})
	require.NoError(t, err)

	info, err := client.EnqueueContext(context.Background(), task)
	require.NoError(t, err)
	assert.Equal(t, tasks.TypePDF, info.Type)

	pending, err := insp.ListPendingTasks("default")
	require.NoError(t, err)
	require.NotEmpty(t, pending, "a tarefa deve estar pendente no Redis")

	found := false
	for _, ti := range pending {
		if ti.Type == tasks.TypePDF {
			found = true
		}
	}
	assert.True(t, found, "a tarefa document:pdf deve estar na fila default")

	// Limpeza.
	_, _ = insp.DeleteAllPendingTasks("default")
}
