package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hibiken/asynq"

	"github.com/joycesilva/acolhe-api/internal/exporter"
	"github.com/joycesilva/acolhe-api/internal/tasks"
)

var (
	ErrLGPDExportNotConfigured = errors.New("pipeline de exportação LGPD não configurado")
	ErrInvalidLGPDExportTask   = errors.New("tarefa de exportação LGPD inválida")
)

type LGPDExportJob struct {
	Status         string
	ObjectKey      string
	ArtifactSHA256 string
	RequestedAt    time.Time
	SLADeadline    time.Time
}

type LGPDExportData struct {
	RecipientEmail string
	PatientName    string
	JSON           []byte
}

// LGPDExportRepository owns the durable state machine. Implementations must
// apply the requester identity to PostgreSQL before reading clinical data.
type LGPDExportRepository interface {
	Get(context.Context, tasks.LGPDExportPayload) (LGPDExportJob, error)
	Start(context.Context, tasks.LGPDExportPayload) error
	LoadData(context.Context, tasks.LGPDExportPayload) (LGPDExportData, error)
	Store(
		ctx context.Context,
		payload tasks.LGPDExportPayload,
		objectKey string,
		artifactSHA256 string,
	) error
	Complete(
		ctx context.Context,
		payload tasks.LGPDExportPayload,
		completedAt time.Time,
	) error
	Fail(
		ctx context.Context,
		payload tasks.LGPDExportPayload,
		failure string,
	) error
}

type ExportObjectStore interface {
	Put(
		ctx context.Context,
		key string,
		contentType string,
		content []byte,
	) error
	PresignGet(key string, expiresIn time.Duration, now time.Time) (string, error)
}

type ExportMailer interface {
	SendExportReady(
		ctx context.Context,
		recipient string,
		patientName string,
		downloadURL string,
		expiresAt time.Time,
	) error
}

// HandleLGPDExport assembles the complete patient export, stores a private ZIP
// containing JSON and PDF, sends a short-lived link, and atomically records
// completion plus the mandatory audit event.
func (w *Workers) HandleLGPDExport(ctx context.Context, task *asynq.Task) error {
	payload, err := decodeLGPDExportTask(task)
	if err != nil {
		return err
	}
	if w.exports == nil || w.store == nil || w.mailer == nil {
		return ErrLGPDExportNotConfigured
	}

	job, err := w.exports.Get(ctx, payload)
	if err != nil {
		return err
	}
	if job.Status == "completed" {
		return nil
	}

	data, err := w.exports.LoadData(ctx, payload)
	if err != nil {
		return w.recordLGPDExportFailure(ctx, payload, "load_data", err)
	}
	if data.RecipientEmail == "" || data.PatientName == "" || !json.Valid(data.JSON) {
		return w.recordLGPDExportFailure(ctx, payload, "validate_data", ErrInvalidLGPDExportTask)
	}

	objectKey := job.ObjectKey
	if job.Status != "stored" {
		if err := w.exports.Start(ctx, payload); err != nil {
			return w.recordLGPDExportFailure(ctx, payload, "start", err)
		}
		archive, err := exporter.BuildLGPDArchive(data.PatientName, payload.RequestedAt, data.JSON)
		if err != nil {
			return w.recordLGPDExportFailure(ctx, payload, "build_archive", err)
		}
		objectKey = fmt.Sprintf(
			"lgpd/%s/%s/%s.zip",
			payload.OrganizationID,
			payload.PatientID,
			payload.RequestID,
		)
		if err := w.store.Put(ctx, objectKey, "application/zip", archive); err != nil {
			return w.recordLGPDExportFailure(ctx, payload, "upload", err)
		}
		digest := sha256.Sum256(archive)
		if err := w.exports.Store(ctx, payload, objectKey, hex.EncodeToString(digest[:])); err != nil {
			return w.recordLGPDExportFailure(ctx, payload, "persist_artifact", err)
		}
	}

	now := w.now()
	const downloadTTL = 24 * time.Hour
	downloadURL, err := w.store.PresignGet(objectKey, downloadTTL, now)
	if err != nil {
		return w.recordLGPDExportFailure(ctx, payload, "sign_download", err)
	}
	if err := w.mailer.SendExportReady(
		ctx,
		data.RecipientEmail,
		data.PatientName,
		downloadURL,
		now.Add(downloadTTL),
	); err != nil {
		return w.recordLGPDExportFailure(ctx, payload, "notify", err)
	}
	if err := w.exports.Complete(ctx, payload, now); err != nil {
		return w.recordLGPDExportFailure(ctx, payload, "complete", err)
	}

	deadline := job.SLADeadline
	if deadline.IsZero() {
		deadline = payload.RequestedAt.Add(24 * time.Hour)
	}
	duration := now.Sub(payload.RequestedAt)
	log.Printf(
		"metric=lgpd_export_duration_seconds value=%.0f request_id=%s sla_breached=%t",
		duration.Seconds(),
		payload.RequestID,
		now.After(deadline),
	)
	return nil
}

func decodeLGPDExportTask(task *asynq.Task) (tasks.LGPDExportPayload, error) {
	var payload tasks.LGPDExportPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return payload, fmt.Errorf("%w: %v", ErrInvalidLGPDExportTask, err)
	}
	if payload.RequestID.String() == "" ||
		payload.RequestID.Version() == 0 ||
		payload.PatientID.Version() == 0 ||
		payload.OrganizationID.Version() == 0 ||
		payload.RequestedBy.Version() == 0 ||
		payload.RequestedAt.IsZero() ||
		(payload.RequesterRole != "patient" && payload.RequesterRole != "psychologist") {
		return payload, ErrInvalidLGPDExportTask
	}
	return payload, nil
}

func (w *Workers) recordLGPDExportFailure(
	ctx context.Context,
	payload tasks.LGPDExportPayload,
	stage string,
	cause error,
) error {
	message := stage + ": " + cause.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	if err := w.exports.Fail(ctx, payload, message); err != nil {
		return errors.Join(cause, fmt.Errorf("persist export failure: %w", err))
	}
	return cause
}
