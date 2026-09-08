package worker_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
	"github.com/joycesilva/acolhe-api/internal/worker"
)

type fakeLGPDRepository struct {
	job       worker.LGPDExportJob
	data      worker.LGPDExportData
	starts    int
	stores    int
	completes int
	failures  []string
}

func (repository *fakeLGPDRepository) Get(
	context.Context,
	tasks.LGPDExportPayload,
) (worker.LGPDExportJob, error) {
	return repository.job, nil
}

func (repository *fakeLGPDRepository) Start(
	context.Context,
	tasks.LGPDExportPayload,
) error {
	repository.starts++
	repository.job.Status = "processing"
	return nil
}

func (repository *fakeLGPDRepository) LoadData(
	context.Context,
	tasks.LGPDExportPayload,
) (worker.LGPDExportData, error) {
	return repository.data, nil
}

func (repository *fakeLGPDRepository) Store(
	_ context.Context,
	_ tasks.LGPDExportPayload,
	objectKey string,
	artifactSHA256 string,
) error {
	repository.stores++
	repository.job.Status = "stored"
	repository.job.ObjectKey = objectKey
	repository.job.ArtifactSHA256 = artifactSHA256
	return nil
}

func (repository *fakeLGPDRepository) Complete(
	_ context.Context,
	_ tasks.LGPDExportPayload,
	_ time.Time,
) error {
	repository.completes++
	repository.job.Status = "completed"
	return nil
}

func (repository *fakeLGPDRepository) Fail(
	_ context.Context,
	_ tasks.LGPDExportPayload,
	failure string,
) error {
	repository.failures = append(repository.failures, failure)
	if repository.job.Status != "stored" {
		repository.job.Status = "failed"
	}
	return nil
}

type fakeObjectStore struct {
	puts       int
	key        string
	content    []byte
	presignURL string
}

func (store *fakeObjectStore) Put(
	_ context.Context,
	key string,
	_ string,
	content []byte,
) error {
	store.puts++
	store.key = key
	store.content = append([]byte(nil), content...)
	return nil
}

func (store *fakeObjectStore) PresignGet(
	_ string,
	_ time.Duration,
	_ time.Time,
) (string, error) {
	return store.presignURL, nil
}

type fakeMailer struct {
	sends int
	err   error
}

func (mailer *fakeMailer) SendExportReady(
	context.Context,
	string,
	string,
	string,
	time.Time,
) error {
	mailer.sends++
	return mailer.err
}

func validLGPDExportPayload(requestedAt time.Time) tasks.LGPDExportPayload {
	return tasks.LGPDExportPayload{
		RequestID: uuid.New(), PatientID: uuid.New(), OrganizationID: uuid.New(),
		RequestedBy: uuid.New(), RequesterRole: "patient", RequestedAt: requestedAt,
	}
}

func TestHandleLGPDExportFullFlowIsResumableAndIdempotent(t *testing.T) {
	requestedAt := time.Date(2026, time.July, 29, 8, 0, 0, 0, time.UTC)
	completedAt := requestedAt.Add(2 * time.Hour)
	payload := validLGPDExportPayload(requestedAt)
	repository := &fakeLGPDRepository{
		job: worker.LGPDExportJob{
			Status: "queued", RequestedAt: requestedAt,
			SLADeadline: requestedAt.Add(24 * time.Hour),
		},
		data: worker.LGPDExportData{
			RecipientEmail: "paciente@example.test",
			PatientName:    "Paciente Teste",
			JSON:           []byte(`{"patient":{"fullName":"Paciente Teste"},"sessions":[{"id":"one"}]}`),
		},
	}
	store := &fakeObjectStore{presignURL: "https://private.example.test/export?signature=one"}
	mailer := &fakeMailer{err: errors.New("SMTP temporariamente indisponível")}
	workers := worker.New(
		&testsupport.FakeQuerier{},
		worker.WithLGPDExport(repository, store, mailer),
		worker.WithClock(func() time.Time { return completedAt }),
	)

	task, err := tasks.NewLGPDExportTask(payload)
	require.NoError(t, err)
	err = workers.HandleLGPDExport(context.Background(), task)
	require.ErrorContains(t, err, "SMTP")
	assert.Equal(t, "stored", repository.job.Status)
	assert.Equal(t, 1, store.puts)
	assert.Len(t, repository.failures, 1)

	mailer.err = nil
	require.NoError(t, workers.HandleLGPDExport(context.Background(), task))
	assert.Equal(t, "completed", repository.job.Status)
	assert.Equal(t, 1, store.puts, "retry após upload não deve recriar o artefato")
	assert.Equal(t, 2, mailer.sends)
	assert.Equal(t, 1, repository.completes)
	assert.Equal(t, "lgpd/"+payload.OrganizationID.String()+"/"+
		payload.PatientID.String()+"/"+payload.RequestID.String()+".zip", store.key)
	assert.Len(t, repository.job.ArtifactSHA256, 64)

	archive, err := zip.NewReader(bytes.NewReader(store.content), int64(len(store.content)))
	require.NoError(t, err)
	require.Len(t, archive.File, 2)
	files := map[string][]byte{}
	for _, file := range archive.File {
		reader, openErr := file.Open()
		require.NoError(t, openErr)
		content, readErr := io.ReadAll(reader)
		require.NoError(t, readErr)
		require.NoError(t, reader.Close())
		files[file.Name] = content
	}
	assert.JSONEq(t, string(repository.data.JSON), string(files["export.json"]))
	assert.True(t, strings.HasPrefix(string(files["export.pdf"]), "%PDF-1.4"))

	require.NoError(t, workers.HandleLGPDExport(context.Background(), task))
	assert.Equal(t, 1, store.puts)
	assert.Equal(t, 2, mailer.sends, "replay concluído deve ser no-op")
	assert.Equal(t, 1, repository.completes)
}

func TestHandleLGPDExportRejectsIncompleteIdentity(t *testing.T) {
	task, err := tasks.NewLGPDExportTask(tasks.LGPDExportPayload{PatientID: uuid.New()})
	require.NoError(t, err)
	err = worker.New(&testsupport.FakeQuerier{}).HandleLGPDExport(context.Background(), task)
	assert.ErrorIs(t, err, worker.ErrInvalidLGPDExportTask)
}

func TestHandleReminder_OK(t *testing.T) {
	w := worker.New(&testsupport.FakeQuerier{})
	task, err := tasks.NewReminderTask(tasks.ReminderPayload{
		AppointmentID: uuid.New(), UserID: uuid.New(),
	})
	require.NoError(t, err)
	assert.NoError(t, w.HandleReminder(context.Background(), task))
}

func TestHandlePDF_OK(t *testing.T) {
	w := worker.New(&testsupport.FakeQuerier{})
	task, err := tasks.NewPDFTask(tasks.PDFPayload{
		DocumentID: uuid.New(), PatientID: uuid.New(), PsychologistID: uuid.New(),
	})
	require.NoError(t, err)
	assert.NoError(t, w.HandlePDF(context.Background(), task))
}

// Construtores devem produzir o tipo correto e payload decodificável.
func TestTaskConstructors(t *testing.T) {
	rt, err := tasks.NewReminderTask(tasks.ReminderPayload{AppointmentID: uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, tasks.TypeReminder, rt.Type())

	pt, err := tasks.NewPDFTask(tasks.PDFPayload{DocumentID: uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, tasks.TypePDF, pt.Type())

	lt, err := tasks.NewLGPDExportTask(tasks.LGPDExportPayload{PatientID: uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, tasks.TypeLGPDExport, lt.Type())
}
