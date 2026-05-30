// Package document cobre documentos clínicos (declarações, recibos). A geração
// de PDF em volume roda em worker assíncrono (spec §7): o service cria o registro
// e enfileira o job document:pdf via asynq; o worker renderiza e preenche pdf_url.
package document

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// ErrPsychologistRequired: usuário autenticado não tem perfil de psicólogo.
var ErrPsychologistRequired = errors.New("ação restrita a psicólogos")

type Service struct {
	q     db.Querier
	queue *asynq.Client // produtor de jobs document:pdf (usado a partir da Fase 6)
}

func NewService(q db.Querier, queue *asynq.Client) *Service {
	return &Service{q: q, queue: queue}
}

// Document é a projeção pública de um documento.
type Document struct {
	ID             uuid.UUID `json:"id"`
	PatientID      uuid.UUID `json:"patientId"`
	PsychologistID uuid.UUID `json:"psychologistId"`
	Type           string    `json:"type"`
	PdfURL         *string   `json:"pdfUrl"`
	CreatedAt      time.Time `json:"createdAt"`
}

// List devolve os documentos de um paciente, isolados por organização.
func (s *Service) List(ctx context.Context, patientID uuid.UUID) ([]Document, error) {
	orgID, err := tenant.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tenant.Queries(ctx, s.q).ListDocumentsByPatient(ctx, db.ListDocumentsByPatientParams{
		PatientID:      patientID,
		OrganizationID: orgID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Document, 0, len(rows))
	for _, r := range rows {
		out = append(out, Document{
			ID:             r.ID,
			PatientID:      r.PatientID,
			PsychologistID: r.PsychologistID,
			Type:           r.Type,
			PdfURL:         r.PdfUrl,
			CreatedAt:      r.CreatedAt,
		})
	}
	return out, nil
}

// GenerateRequest é o corpo de POST /documents/generate.
type GenerateRequest struct {
	PatientID uuid.UUID `json:"patientId"`
	Type      string    `json:"type"` // ex.: "declaration", "receipt"
}

// GeneratePDF cria o registro do documento (sem pdf_url ainda) e enfileira o job
// document:pdf para o worker renderizar e preencher o pdf_url depois.
func (s *Service) GeneratePDF(ctx context.Context, req GenerateRequest) (*Document, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, tenant.ErrNoTenant
	}
	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return nil, ErrPsychologistRequired
	}

	row, err := q.CreateDocument(ctx, db.CreateDocumentParams{
		PatientID:      req.PatientID,
		PsychologistID: psy.ID,
		TemplateID:     nil,
		Type:           req.Type,
		PdfUrl:         nil,
	})
	if err != nil {
		return nil, err
	}

	if s.queue != nil {
		task, terr := tasks.NewPDFTask(tasks.PDFPayload{
			DocumentID:     row.ID,
			PatientID:      row.PatientID,
			PsychologistID: row.PsychologistID,
		})
		if terr == nil {
			_, _ = s.queue.EnqueueContext(ctx, task)
		}
	}

	return &Document{
		ID:             row.ID,
		PatientID:      row.PatientID,
		PsychologistID: row.PsychologistID,
		Type:           row.Type,
		PdfURL:         row.PdfUrl,
		CreatedAt:      row.CreatedAt,
	}, nil
}
