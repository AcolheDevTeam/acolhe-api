// Package worker contém os handlers das tarefas assíncronas (asynq). Cada handler
// recebe um *asynq.Task, decodifica o payload e executa o job. Os workers rodam
// em um processo separado (cmd/worker), consumindo do Redis (spec §4.5, §7).
//
// Integrações externas (S3, e-mail, render de PDF) ficam marcadas como TODO —
// estão fora do escopo de infra deste passo; o esqueleto e o fluxo estão prontos.
package worker

import (
	"context"
	"encoding/json"
	"log"

	"github.com/hibiken/asynq"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tasks"
)

// Workers agrupa os handlers e suas dependências (acesso a dados).
type Workers struct {
	q db.Querier
}

func New(q db.Querier) *Workers {
	return &Workers{q: q}
}

// Register pendura todos os handlers no mux do asynq.
func (w *Workers) Register(mux *asynq.ServeMux) {
	mux.HandleFunc(tasks.TypeLGPDExport, w.HandleLGPDExport)
	mux.HandleFunc(tasks.TypeReminder, w.HandleReminder)
	mux.HandleFunc(tasks.TypePDF, w.HandlePDF)
}

// HandleLGPDExport: reúne os dados do paciente, gera PDF + JSON, sobe para o S3,
// notifica o solicitante e registra a auditoria (spec §7, SLA 24h).
func (w *Workers) HandleLGPDExport(ctx context.Context, t *asynq.Task) error {
	var p tasks.LGPDExportPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err // payload corrompido: não adianta retentar
	}
	log.Printf("[lgpd:export] iniciando exportação do paciente %s", p.PatientID)

	// TODO(Fase infra): fetchAllPatientData → generatePDF + generateJSON →
	// uploadToS3 → notifyByEmail. Por ora registramos a auditoria (append-only).
	// OrganizationID nil: job de sistema, sem org no contexto.
	if err := w.q.WriteAuditLog(ctx, db.WriteAuditLogParams{
		ActorUserID:    p.RequestedBy,
		OrganizationID: nil,
		Action:         "lgpd_export",
		ResourceType:   "patient",
		ResourceID:     p.PatientID.String(),
	}); err != nil {
		return err // falhou a auditoria: retenta (audit é obrigatório p/ LGPD)
	}
	log.Printf("[lgpd:export] concluído (paciente %s)", p.PatientID)
	return nil
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
