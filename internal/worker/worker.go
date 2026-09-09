// Package worker contém os handlers das tarefas assíncronas (asynq). Cada handler
// recebe um *asynq.Task, decodifica o payload e executa o job. Os workers rodam
// em um processo separado (cmd/worker), consumindo do Redis (spec §4.5, §7).
package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/hibiken/asynq"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tasks"
)

// Workers agrupa os handlers e suas dependências (acesso a dados).
type Workers struct {
	q       db.Querier
	exports LGPDExportRepository
	store   ExportObjectStore
	mailer  ExportMailer
	now     func() time.Time
}

type Option func(*Workers)

func WithLGPDExport(
	repository LGPDExportRepository,
	store ExportObjectStore,
	mailer ExportMailer,
) Option {
	return func(workers *Workers) {
		workers.exports = repository
		workers.store = store
		workers.mailer = mailer
	}
}

func WithClock(now func() time.Time) Option {
	return func(workers *Workers) {
		workers.now = now
	}
}

func New(q db.Querier, options ...Option) *Workers {
	workers := &Workers{q: q, now: func() time.Time { return time.Now().UTC() }}
	for _, option := range options {
		option(workers)
	}
	return workers
}

// Register pendura todos os handlers no mux do asynq.
func (w *Workers) Register(mux *asynq.ServeMux) {
	mux.HandleFunc(tasks.TypeLGPDExport, w.HandleLGPDExport)
	mux.HandleFunc(tasks.TypeReminder, w.HandleReminder)
	mux.HandleFunc(tasks.TypePDF, w.HandlePDF)
}

// HandleReminder envia o lembrete do agendamento.
func (w *Workers) HandleReminder(ctx context.Context, t *asynq.Task) error {
	var p tasks.ReminderPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}
	// TODO(Fase infra): enviar via canal (push/e-mail) e gravar em notification.
	log.Printf("[notification:reminder] lembrete p/ usuário %s do agendamento %s em %s",
		p.UserID, p.AppointmentID, p.ScheduledFor.Format("2006-01-02 15:04"))
	return nil
}

// HandlePDF gera o documento (declaração/recibo) e atualiza pdf_url.
func (w *Workers) HandlePDF(ctx context.Context, t *asynq.Task) error {
	var p tasks.PDFPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}
	// TODO(Fase infra): renderizar template → PDF → upload → update document.pdf_url.
	log.Printf("[document:pdf] gerando documento %s (paciente %s)", p.DocumentID, p.PatientID)
	return nil
}
