package session

import (
	"time"

	"github.com/google/uuid"
)

// CreateRequest é o corpo de POST /sessions.
type CreateRequest struct {
	PatientID  uuid.UUID `json:"patientId"`
	OccurredAt time.Time `json:"occurredAt"`
	Notes      string    `json:"notes"`
}

// Session é a projeção pública de uma sessão clínica.
type Session struct {
	ID             uuid.UUID `json:"id"`
	PatientID      uuid.UUID `json:"patientId"`
	PatientName    string    `json:"patientName,omitempty"`
	PsychologistID uuid.UUID `json:"psychologistId"`
	OccurredAt     time.Time `json:"occurredAt"`
	Status         string    `json:"status"`
	Notes          string    `json:"notes,omitempty"`
	Modality       *string   `json:"modality,omitempty"`
	DurationMin    *int32    `json:"durationMin,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// TimelineItem é uma entrada da timeline unificada do paciente.
type TimelineItem struct {
	Kind       string    `json:"kind"` // session | appointment | activity
	ItemID     uuid.UUID `json:"itemId"`
	OccurredAt time.Time `json:"occurredAt"`
	Status     string    `json:"status"`
}
