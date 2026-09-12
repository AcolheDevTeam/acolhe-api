//go:build integration

// Testes de service com Postgres real (testcontainers) — sem mocks de banco.
// Rodam com Docker disponível:
//
//	go test -tags=integration ./internal/session/...
//
// O teste-bandeira é o isolamento multi-tenant: a organização 2 não pode
// enxergar dados da organização 1 (spec §8).
package session_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/session"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// setupDB sobe um Postgres efêmero, aplica o schema.sql e devolve o pool.
func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("acolhe_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "db", "schema.sql")
	schema, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(schema))
	require.NoError(t, err)

	return pool
}

// fixtures cria org + usuário + psicólogo + paciente + sessão e devolve os ids.
func seedSession(t *testing.T, pool *pgxpool.Pool) (orgID, userID, patientID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name, slug) VALUES ('Org 1', 'org-1') RETURNING id`).Scan(&orgID))

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (organization_id, email, password_hash, role)
		 VALUES ($1, 'psi@org1.dev', 'x', 'psychologist') RETURNING id`, orgID).Scan(&userID))

	var psyID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
		 VALUES ($1, 'Dra. Org1', '111', 'SP') RETURNING id`, userID).Scan(&psyID))

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO patient_profile (organization_id, full_name) VALUES ($1, 'Paciente Org1') RETURNING id`,
		orgID).Scan(&patientID))

	// O gate de consentimento (migration 20260729011635) exige vínculo ativo antes
	// de qualquer sessão. Mesmo padrão das fixtures de internal/app.
	_, err := pool.Exec(ctx,
		`INSERT INTO patient_relationship (
		   patient_id, psychologist_id, status, requires_health_consent
		 ) VALUES ($1, $2, 'active', false)`, patientID, psyID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO session (patient_id, psychologist_id, occurred_at, status)
		 VALUES ($1, $2, now(), 'completed')`, patientID, psyID)
	require.NoError(t, err)

	return orgID, userID, patientID
}

// TestGetSessionsByPatient_OrgIsolation: org2 não enxerga sessões da org1.
func TestGetSessionsByPatient_OrgIsolation(t *testing.T) {
	pool := setupDB(t)
	q := db.New(pool)
	svc := session.NewService(q)

	org1, user1, patientID := seedSession(t, pool)

	// org1 vê a sessão do seu paciente.
	ctx1 := tenant.WithIdentity(context.Background(), tenant.Identity{OrgID: org1, UserID: user1, Role: "psychologist"})
	got1, err := svc.List(ctx1, patientID)
	require.NoError(t, err)
	assert.Len(t, got1, 1, "org1 deve ver a própria sessão")

	// org2 (outra organização) NÃO pode ver dados da org1.
	var org2, user2 uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO organization (name, slug) VALUES ('Org 2', 'org-2') RETURNING id`).Scan(&org2))
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO "user" (organization_id, email, password_hash, role)
		 VALUES ($1, 'psi@org2.dev', 'x', 'psychologist') RETURNING id`, org2).Scan(&user2))
	_, err = pool.Exec(context.Background(),
		`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
		 VALUES ($1, 'Dra. Org2', '222', 'SP')`, user2)
	require.NoError(t, err)
	ctx2 := tenant.WithIdentity(context.Background(), tenant.Identity{OrgID: org2, UserID: user2, Role: "psychologist"})
	got2, err := svc.List(ctx2, patientID)
	require.NoError(t, err)
	assert.Empty(t, got2, "org2 não pode enxergar dados da org1")
}
