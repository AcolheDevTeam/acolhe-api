package worker_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
	"github.com/joycesilva/acolhe-api/internal/worker"
)

// HandleLGPDExport deve gravar uma entrada de auditoria "lgpd_export".
func TestHandleLGPDExport_WritesAudit(t *testing.T) {
	patientID := uuid.New()
	requestedBy := uuid.New()

	var logged db.WriteAuditLogParams
	fake := &testsupport.FakeQuerier{
		WriteAuditLogFn: func(_ context.Context, arg db.WriteAuditLogParams) error {
			logged = arg
			return nil
		},
	}
	w := worker.New(fake)

	task, err := tasks.NewLGPDExportTask(tasks.LGPDExportPayload{PatientID: patientID, RequestedBy: requestedBy})
	require.NoError(t, err)

	require.NoError(t, w.HandleLGPDExport(context.Background(), task))
	assert.Equal(t, "lgpd_export", logged.Action)
	assert.Equal(t, "patient", logged.ResourceType)
	assert.Equal(t, patientID.String(), logged.ResourceID)
	assert.Equal(t, requestedBy, logged.ActorUserID)
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
