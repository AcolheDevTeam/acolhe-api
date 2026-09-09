// Package notification enfileira notificações ao usuário. O envio efetivo e os
// lembretes rodam em worker assíncrono (notification:reminder) — o service só
// produz a tarefa via asynq (spec §4.5, §7).
package notification

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	taskqueue "github.com/joycesilva/acolhe-api/internal/queue"
	"github.com/joycesilva/acolhe-api/internal/tasks"
)

// ErrQueueUnavailable: cliente de fila não configurado (ex.: teste sem Redis).
var ErrQueueUnavailable = errors.New("fila de tarefas indisponível")
var ErrInvalidInput = errors.New("dados do lembrete inválidos")

type Service struct {
	queue taskqueue.Enqueuer
}

func NewService(queue taskqueue.Enqueuer) *Service {
	return &Service{queue: queue}
}

// ReminderRequest é o corpo de POST /notifications/reminders.
type ReminderRequest struct {
	AppointmentID uuid.UUID `json:"appointmentId"`
	UserID        uuid.UUID `json:"userId"`
	ScheduledFor  time.Time `json:"scheduledFor"`
}

// EnqueueReminder agenda um lembrete. Se ScheduledFor for futuro, agenda o
// processamento para algum tempo antes do horário (aqui: na hora informada).
func (s *Service) EnqueueReminder(ctx context.Context, req ReminderRequest) error {
	if req.AppointmentID == uuid.Nil || req.UserID == uuid.Nil || req.ScheduledFor.IsZero() {
		return ErrInvalidInput
	}
	if s.queue == nil {
		return ErrQueueUnavailable
	}
	task, err := tasks.NewReminderTask(tasks.ReminderPayload{
		AppointmentID: req.AppointmentID,
		UserID:        req.UserID,
		ScheduledFor:  req.ScheduledFor,
	})
	if err != nil {
		return err
	}
	_, err = s.queue.EnqueueContext(ctx, task)
	return err
}
