//go:build integration

// Round-trip real no Redis (asynq): enfileira, reinicia o Redis com AOF e
// processa a tarefa pelo handler real do worker.
//
//	go test -tags=integration ./internal/worker/...
package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
	"github.com/joycesilva/acolhe-api/internal/worker"
)

func TestQueueSurvivesRedisRestartAndProcessesTask(t *testing.T) {
	ctx := context.Background()
	redis, err := testcontainers.Run(
		ctx,
		"redis:7-alpine",
		testcontainers.WithExposedPorts("6379/tcp"),
		testcontainers.WithCmd(
			"redis-server",
			"--save", "",
			"--appendonly", "yes",
			"--appendfsync", "always",
		),
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(redis) })

	host, err := redis.Host(ctx)
	require.NoError(t, err)
	port, err := redis.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	opt := asynq.RedisClientOpt{Addr: host + ":" + port.Port()}

	client := asynq.NewClient(opt)

	task, err := tasks.NewPDFTask(tasks.PDFPayload{
		DocumentID: uuid.New(), PatientID: uuid.New(), PsychologistID: uuid.New(),
	})
	require.NoError(t, err)

	info, err := client.EnqueueContext(context.Background(), task)
	require.NoError(t, err)
	assert.Equal(t, tasks.TypePDF, info.Type)

	inspector := asynq.NewInspector(opt)
	pending, err := inspector.ListPendingTasks("default")
	require.NoError(t, err)
	require.NotEmpty(t, pending, "a tarefa deve estar pendente no Redis")
	require.NoError(t, inspector.Close())
	require.NoError(t, client.Close())

	require.NoError(t, redis.Stop(ctx, nil))
	require.NoError(t, redis.Start(ctx))

	host, err = redis.Host(ctx)
	require.NoError(t, err)
	port, err = redis.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	opt = asynq.RedisClientOpt{Addr: host + ":" + port.Port()}

	require.Eventually(t, func() bool {
		restartedInspector := asynq.NewInspector(opt)
		defer restartedInspector.Close()
		afterRestart, inspectErr := restartedInspector.ListPendingTasks("default")
		return inspectErr == nil && len(afterRestart) == 1 && afterRestart[0].Type == tasks.TypePDF
	}, 10*time.Second, 100*time.Millisecond, "o AOF deve restaurar a tarefa após reiniciar o Redis")

	processed := make(chan struct{}, 1)
	workers := worker.New(&testsupport.FakeQuerier{})
	mux := asynq.NewServeMux()
	mux.HandleFunc(tasks.TypePDF, func(ctx context.Context, task *asynq.Task) error {
		if err := workers.HandlePDF(ctx, task); err != nil {
			return err
		}
		processed <- struct{}{}
		return nil
	})

	server := asynq.NewServer(opt, asynq.Config{Concurrency: 1})
	go func() {
		_ = server.Run(mux)
	}()
	t.Cleanup(server.Shutdown)

	select {
	case <-processed:
	case <-time.After(10 * time.Second):
		t.Fatal("o worker não processou a tarefa restaurada")
	}
}
