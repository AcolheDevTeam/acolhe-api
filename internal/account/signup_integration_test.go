//go:build integration

package account_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/joycesilva/acolhe-api/internal/app"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

func signupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16", postgres.WithDatabase("acolhe_test"),
		postgres.WithUsername("test"), postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
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

func signupRequest(t *testing.T, srv *httptest.Server, email, crp string) *http.Response {
	t.Helper()
	body := `{"email":"` + email + `","password":"senha-segura-123","fullName":"Mariana Sá","crpNumber":"` + crp + `","crpState":"06","acceptTerms":true,"termsVersion":"0.3"}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/signup", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	return resp
}

func TestSignup_AtomicAndSafeResponse(t *testing.T) {
	ctx := context.Background()
	pool := signupPool(t)
	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)

	resp := signupRequest(t, srv, " PSI@Example.COM ", "123456")
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.NotEmpty(t, out["token"])
	assert.Equal(t, "pending", out["crpStatus"])
	assert.NotContains(t, string(mustJSON(t, out)), "senha-segura-123")

	var organizations, users, profiles, consents, passwordHash string
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*)::text FROM organization`).Scan(&organizations))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*)::text FROM "user"`).Scan(&users))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*)::text FROM psychologist_profile`).Scan(&profiles))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*)::text FROM consent`).Scan(&consents))
	require.NoError(t, pool.QueryRow(ctx, `SELECT password_hash FROM "user"`).Scan(&passwordHash))
	assert.Equal(t, "1", organizations)
	assert.Equal(t, "1", users)
	assert.Equal(t, "1", profiles)
	assert.Equal(t, "1", consents)
	assert.True(t, strings.HasPrefix(passwordHash, "$2"))
}

func TestSignup_ReplayAndConcurrentDuplicateAreSafe(t *testing.T) {
	pool := signupPool(t)
	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)

	first := signupRequest(t, srv, "same@example.com", "123456")
	first.Body.Close()
	replay := signupRequest(t, srv, "SAME@example.com", "123456")
	replay.Body.Close()
	assert.Equal(t, http.StatusConflict, replay.StatusCode)

	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := signupRequest(t, srv, "race@example.com", "654321")
			statuses <- resp.StatusCode
			resp.Body.Close()
		}()
	}
	wg.Wait()
	close(statuses)
	var created, conflicts int
	for status := range statuses {
		if status == http.StatusCreated {
			created++
		}
		if status == http.StatusConflict {
			conflicts++
		}
	}
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, conflicts)
}

func TestSignup_PreexistingCRPRollsBackOrganization(t *testing.T) {
	ctx := context.Background()
	pool := signupPool(t)
	_, err := pool.Exec(ctx, `INSERT INTO organization (name, slug) VALUES ('Existente', 'existente')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO "user" (organization_id, email, password_hash, role) SELECT id, 'old@example.com', 'x', 'psychologist' FROM organization`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state) SELECT id, 'Existente', '123456', '06' FROM "user"`)
	require.NoError(t, err)
	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)

	resp := signupRequest(t, srv, "new@example.com", "123456")
	resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	var organizations int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM organization`).Scan(&organizations))
	assert.Equal(t, 1, organizations)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return b
}
