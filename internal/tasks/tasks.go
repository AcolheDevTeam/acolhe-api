// Package tasks define o contrato das tarefas assíncronas (asynq): nomes de tipo
// e payloads. É compartilhado entre os produtores (services que enfileiram) e os
// consumidores (workers), garantindo um único ponto de verdade para o formato
// das mensagens (spec §7).
package tasks

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// Tipos de tarefa (spec §7).
const (
	TypeLGPDExport = "lgpd:export"           // SLA 24h (requisito LGPD)
	TypeReminder   = "notification:reminder" // lembrete antes do agendamento
	TypePDF        = "document:pdf"          // geração de declaração/recibo
)

// --- payloads ---

type LGPDExportPayload struct {
	PatientID   uuid.UUID `json:"patientId"`
	RequestedBy uuid.UUID `json:"requestedBy"`
}

type ReminderPayload struct {
	AppointmentID uuid.UUID `json:"appointmentId"`
	UserID        uuid.UUID `json:"userId"`
	ScheduledFor  time.Time `json:"scheduledFor"`
}

type PDFPayload struct {
	DocumentID     uuid.UUID `json:"documentId"`
	PatientID      uuid.UUID `json:"patientId"`
	PsychologistID uuid.UUID `json:"psychologistId"`
}

// --- construtores (usados pelos produtores) ---

// NewLGPDExportTask agenda a exportação LGPD com retenção de 24h (SLA do PRD).
func NewLGPDExportTask(p LGPDExportPayload) (*asynq.Task, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeLGPDExport, b, asynq.MaxRetry(5), asynq.Timeout(24*time.Hour)), nil
}

// NewReminderTask agenda um lembrete; opcionalmente para um instante futuro.
func NewReminderTask(p ReminderPayload, opts ...asynq.Option) (*asynq.Task, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeReminder, b, opts...), nil
}

// NewPDFTask enfileira a geração de um documento em PDF.
func NewPDFTask(p PDFPayload) (*asynq.Task, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypePDF, b, asynq.MaxRetry(3)), nil
}
