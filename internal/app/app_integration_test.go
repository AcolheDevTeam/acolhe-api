//go:build integration

// Teste de integração do stack completo (Echo + middlewares Auth/Tenant/Audit/
// TenantTx + services + sqlc) contra Postgres real (testcontainers).
//
//	go test -tags=integration ./internal/app/...
//
// Valida a Fase 5: a transação por requisição com SET LOCAL acolhe.* comita, e o
// middleware de audit grava em audit_log de forma assíncrona.
package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/joycesilva/acolhe-api/internal/app"
	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("acolhe_test"),
		postgres.WithUsername("test"), postgres.WithPassword("test"),
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
	schema, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "db", "schema.sql"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(schema))
	require.NoError(t, err)
	return pool
}

// TestCreateSession_FullStack_AuditAndTx exercita POST /sessions ponta a ponta.
func TestCreateSession_FullStack_AuditAndTx(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)

	// Fixtures: org + psicólogo + paciente + vínculo ativo.
	var orgID, userID, psyID, patientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name, slug) VALUES ('Org', 'org') RETURNING id`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (organization_id, email, password_hash, role)
		 VALUES ($1,'psi@org.dev','x','psychologist') RETURNING id`, orgID).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
		 VALUES ($1,'Dra.','1','SP') RETURNING id`, userID).Scan(&psyID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO patient_profile (organization_id, full_name) VALUES ($1,'Paciente') RETURNING id`,
		orgID).Scan(&patientID))
	_, err := pool.Exec(ctx,
		`INSERT INTO patient_relationship (patient_id, psychologist_id, status)
		 VALUES ($1,$2,'active')`, patientID, psyID)
	require.NoError(t, err)

	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)

	token, err := auth.GenerateToken("secret", userID.String(), "psychologist", orgID.String())
	require.NoError(t, err)

	body := `{"patientId":"` + patientID.String() + `","occurredAt":"2026-05-29T10:00:00Z"}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// TenantTx comitou a sessão criada dentro da transação com SET LOCAL.
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var sessions int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM session WHERE patient_id=$1`, patientID).Scan(&sessions))
	assert.Equal(t, 1, sessions, "sessão deve ter sido persistida (commit da tx)")

	// O middleware Audit grava em audit_log de forma assíncrona — aguardamos.
	var audits int
	require.Eventually(t, func() bool {
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE actor_user_id=$1 AND resource_type='sessions'`,
			userID).Scan(&audits)
		return audits == 1
	}, 3*time.Second, 50*time.Millisecond, "audit_log deve registrar a mutação")
}
