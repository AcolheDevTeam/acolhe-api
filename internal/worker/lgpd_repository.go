package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tasks"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

type PostgresLGPDExportRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresLGPDExportRepository(pool *pgxpool.Pool) *PostgresLGPDExportRepository {
	return &PostgresLGPDExportRepository{pool: pool}
}

func (repository *PostgresLGPDExportRepository) Get(
	ctx context.Context,
	payload tasks.LGPDExportPayload,
) (LGPDExportJob, error) {
	var job LGPDExportJob
	err := repository.withQueries(ctx, payload, func(queries *db.Queries) error {
		row, err := queries.GetLGPDExportRequest(ctx, exportRequestParams(payload))
		if err != nil {
			return err
		}
		job = exportJob(row)
		return nil
	})
	return job, err
}

func (repository *PostgresLGPDExportRepository) Start(
	ctx context.Context,
	payload tasks.LGPDExportPayload,
) error {
	return repository.withQueries(ctx, payload, func(queries *db.Queries) error {
		affected, err := queries.MarkLGPDExportProcessing(
			ctx,
			db.MarkLGPDExportProcessingParams{
				ID: payload.RequestID, PatientID: payload.PatientID,
				OrganizationID: payload.OrganizationID, RequestedBy: payload.RequestedBy,
			},
		)
		return exactlyOne("start LGPD export", affected, err)
	})
}

func (repository *PostgresLGPDExportRepository) LoadData(
	ctx context.Context,
	payload tasks.LGPDExportPayload,
) (LGPDExportData, error) {
	var data LGPDExportData
	err := repository.withQueries(ctx, payload, func(queries *db.Queries) error {
		row, err := queries.GetPatientLGPDExportData(
			ctx,
			db.GetPatientLGPDExportDataParams{
				ID: payload.RequestID, PatientID: payload.PatientID,
				OrganizationID: payload.OrganizationID, RequestedBy: payload.RequestedBy,
			},
		)
		if err != nil {
			return err
		}
		data = LGPDExportData{
			RecipientEmail: row.RecipientEmail,
			PatientName:    row.PatientName,
			JSON:           row.ExportData,
		}
		return nil
	})
	return data, err
}

func (repository *PostgresLGPDExportRepository) Store(
	ctx context.Context,
	payload tasks.LGPDExportPayload,
	objectKey string,
	artifactSHA256 string,
) error {
	return repository.withQueries(ctx, payload, func(queries *db.Queries) error {
		affected, err := queries.MarkLGPDExportStored(ctx, db.MarkLGPDExportStoredParams{
			ObjectKey: &objectKey, ArtifactSha256: &artifactSHA256,
			ID: payload.RequestID, RequestedBy: payload.RequestedBy,
		})
		return exactlyOne("store LGPD export", affected, err)
	})
}

func (repository *PostgresLGPDExportRepository) Complete(
	ctx context.Context,
	payload tasks.LGPDExportPayload,
	completedAt time.Time,
) error {
	return repository.withQueries(ctx, payload, func(queries *db.Queries) error {
		request, err := queries.GetLGPDExportRequest(ctx, exportRequestParams(payload))
		if err != nil {
			return err
		}
		if request.Status == "completed" {
			return nil
		}
		affected, err := queries.MarkLGPDExportCompleted(ctx, db.MarkLGPDExportCompletedParams{
			CompletedAt: &completedAt, NotifiedAt: &completedAt,
			ID: payload.RequestID, RequestedBy: payload.RequestedBy,
		})
		if err := exactlyOne("complete LGPD export", affected, err); err != nil {
			return err
		}
		metadata, err := json.Marshal(map[string]any{
			"requestId":      payload.RequestID,
			"objectKey":      request.ObjectKey,
			"artifactSha256": request.ArtifactSha256,
			"requestedAt":    request.RequestedAt,
			"slaDeadline":    request.SlaDeadline,
			"completedAt":    completedAt,
			"slaBreached":    completedAt.After(request.SlaDeadline),
		})
		if err != nil {
			return err
		}
		organizationID := payload.OrganizationID
		return queries.WriteAuditLog(ctx, db.WriteAuditLogParams{
			ActorUserID: payload.RequestedBy, OrganizationID: &organizationID,
			Action: "lgpd_export", ResourceType: "patient",
			ResourceID: payload.PatientID.String(), MetadataJsonb: metadata,
		})
	})
}

func (repository *PostgresLGPDExportRepository) Fail(
	ctx context.Context,
	payload tasks.LGPDExportPayload,
	failure string,
) error {
	return repository.withQueries(ctx, payload, func(queries *db.Queries) error {
		affected, err := queries.MarkLGPDExportFailed(ctx, db.MarkLGPDExportFailedParams{
			LastError: &failure, ID: payload.RequestID, RequestedBy: payload.RequestedBy,
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			request, getErr := queries.GetLGPDExportRequest(ctx, exportRequestParams(payload))
			if getErr == nil && request.Status == "completed" {
				return nil
			}
			return errors.New("LGPD export failure state was not persisted")
		}
		return nil
	})
}

func (repository *PostgresLGPDExportRepository) withQueries(
	ctx context.Context,
	payload tasks.LGPDExportPayload,
	run func(*db.Queries) error,
) error {
	if repository.pool == nil {
		return errors.New("PostgreSQL pool is nil")
	}
	identity := tenant.Identity{
		UserID: payload.RequestedBy, OrgID: payload.OrganizationID,
		Role: payload.RequesterRole,
	}
	tx, queries, err := tenant.BeginTransaction(ctx, repository.pool, identity)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if err := run(queries); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

func exportRequestParams(payload tasks.LGPDExportPayload) db.GetLGPDExportRequestParams {
	return db.GetLGPDExportRequestParams{
		ID: payload.RequestID, PatientID: payload.PatientID,
		OrganizationID: payload.OrganizationID, RequestedBy: payload.RequestedBy,
	}
}

func exportJob(row db.LgpdExportRequest) LGPDExportJob {
	job := LGPDExportJob{
		Status: row.Status, RequestedAt: row.RequestedAt, SLADeadline: row.SlaDeadline,
	}
	if row.ObjectKey != nil {
		job.ObjectKey = *row.ObjectKey
	}
	if row.ArtifactSha256 != nil {
		job.ArtifactSHA256 = *row.ArtifactSha256
	}
	return job
}

func exactlyOne(operation string, affected int64, err error) error {
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%s affected %d rows", operation, affected)
	}
	return nil
}
