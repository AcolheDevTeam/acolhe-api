package tenant

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

// BeginTransaction opens a tenant-scoped transaction and applies every
// PostgreSQL setting consumed by the RLS policies. HTTP requests and background
// jobs use this same boundary so workers cannot accidentally bypass or disable
// tenant isolation.
func BeginTransaction(
	ctx context.Context,
	pool *pgxpool.Pool,
	identity Identity,
) (pgx.Tx, *db.Queries, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	queries := db.New(tx)
	if err := ApplyRLSContext(ctx, tx, queries, identity); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return tx, queries, nil
}

// ApplyRLSContext sets transaction-local values read by current_* helpers in
// schema.sql. Callers must never reuse the transaction for another identity.
func ApplyRLSContext(
	ctx context.Context,
	tx pgx.Tx,
	queries *db.Queries,
	identity Identity,
) error {
	if err := setConfig(ctx, tx, "acolhe.user_id", identity.UserID.String()); err != nil {
		return err
	}
	if err := setConfig(ctx, tx, "acolhe.user_role", identity.Role); err != nil {
		return err
	}
	if identity.OrgID != uuid.Nil {
		if err := setConfig(ctx, tx, "acolhe.organization_id", identity.OrgID.String()); err != nil {
			return err
		}
	}
	if identity.Role == "psychologist" {
		if psychologist, err := queries.GetPsychologistByUser(ctx, identity.UserID); err == nil {
			if err := setConfig(ctx, tx, "acolhe.psychologist_id", psychologist.ID.String()); err != nil {
				return err
			}
		}
	}
	return nil
}

func setConfig(ctx context.Context, tx pgx.Tx, key, value string) error {
	_, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", key, value)
	return err
}
