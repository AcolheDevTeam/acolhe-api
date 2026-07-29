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
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func doJSON(t *testing.T, client *http.Client, method, url, token string, body any) *http.Response {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&payload).Encode(body))
	}
	req, err := http.NewRequest(method, url, &payload)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

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
	_, err = pool.Exec(ctx, `
		WITH documents(scope, version, title, content, required) AS (
		  VALUES
		    ('health_data', '0.3', 'Dados de saúde',
		     'Você autoriza que sua psicóloga registre prontuário, atividades e respostas, conforme Resolução CFP 01/2009.', true),
		    ('communications', '0.3', 'Comunicações',
		     'Receber lembretes de sessão e atividades por e-mail (sem conteúdo sensível no corpo da mensagem).', false),
		    ('aggregate_statistics', '0.3', 'Estatística agregada',
		     'Uso anônimo do Acolhe para métricas operacionais. Nunca cruzado com dados clínicos.', false)
		)
		INSERT INTO consent_document (
		  scope, version, title, content, content_sha256, required, published_at
		)
		SELECT scope, version, title, content,
		       encode(digest(content, 'sha256'), 'hex'), required,
		       '2026-05-12T00:00:00-03:00'::timestamptz
		FROM documents`)
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
		`INSERT INTO patient_relationship (
		   patient_id, psychologist_id, status, requires_health_consent
		 ) VALUES ($1,$2,'active',false)`, patientID, psyID)
	require.NoError(t, err)

	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)

	token, err := auth.GenerateToken("secret", userID.String(), "psychologist", orgID.String())
	require.NoError(t, err)

	body := `{"patientId":"` + patientID.String() + `","occurredAt":"2026-05-29T10:00:00Z","notes":"Evolução"}`
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

func TestClinicalBFFContracts_FullStack(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)

	var orgID, userID, psyID, typeID, templateID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name, slug) VALUES ('Org BFF', 'org-bff') RETURNING id`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (organization_id, email, password_hash, role)
		 VALUES ($1,'bff@org.dev','x','psychologist') RETURNING id`, orgID).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
		 VALUES ($1,'Dra. BFF','2','SP') RETURNING id`, userID).Scan(&psyID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO activity_type (code, name) VALUES ('record','Registro') RETURNING id`).Scan(&typeID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO activity_template (type_id, organization_id, author_id, title)
		 VALUES ($1,$2,$3,'Registro diário') RETURNING id`, typeID, orgID, psyID).Scan(&templateID))

	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)
	token, err := auth.GenerateToken("secret", userID.String(), "psychologist", orgID.String())
	require.NoError(t, err)

	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/patients", token, map[string]any{
		"fullName": "Paciente BFF", "email": "paciente-bff@example.test", "birthDate": "1990-01-02",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var patient struct {
		ID                 uuid.UUID `json:"id"`
		Status             string    `json:"status"`
		RelationshipStatus string    `json:"relationshipStatus"`
		Invitation         struct {
			Token string `json:"token"`
		} `json:"invitation"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&patient))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "onboarding", patient.Status)
	assert.Equal(t, "pending", patient.RelationshipStatus)
	require.NotEmpty(t, patient.Invitation.Token)
	var relationships int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM patient_relationship WHERE patient_id=$1 AND psychologist_id=$2 AND status='pending'`,
		patient.ID, psyID).Scan(&relationships))
	assert.Equal(t, 1, relationships)

	// No clinical write is available before the patient accepts the required
	// versioned health-data consent.
	resp = doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/sessions", token, map[string]any{
		"patientId": patient.ID, "occurredAt": "2026-07-28T10:00:00Z", "notes": "Evolução clínica",
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
	resp = doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/activities", token, map[string]any{
		"templateId": templateID, "patientId": patient.ID,
	})
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	resp = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/onboarding/invitations/"+patient.Invitation.Token, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var invitation struct {
		Documents []struct {
			ID       uuid.UUID `json:"id"`
			Required bool      `json:"required"`
		} `json:"documents"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&invitation))
	require.NoError(t, resp.Body.Close())
	var acceptedDocumentIDs []uuid.UUID
	for _, document := range invitation.Documents {
		acceptedDocumentIDs = append(acceptedDocumentIDs, document.ID)
	}
	require.NotEmpty(t, acceptedDocumentIDs)

	resp = doJSON(t, srv.Client(), http.MethodPost,
		srv.URL+"/onboarding/invitations/"+patient.Invitation.Token+"/accept", "", map[string]any{
			"password":            "segredo-forte",
			"acceptedDocumentIds": acceptedDocumentIDs,
		})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	// Replaying an accepted invitation is idempotent: it returns the original
	// identities and does not append duplicate consents or audit evidence.
	resp = doJSON(t, srv.Client(), http.MethodPost,
		srv.URL+"/onboarding/invitations/"+patient.Invitation.Token+"/accept", "", map[string]any{
			"password":            "segredo-forte",
			"acceptedDocumentIds": acceptedDocumentIDs,
		})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var replay struct {
		AlreadyAccepted bool `json:"alreadyAccepted"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&replay))
	require.NoError(t, resp.Body.Close())
	assert.True(t, replay.AlreadyAccepted)

	var patientStatus, relationshipStatus string
	var consentCount, onboardingAuditCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT p.status, r.status
		 FROM patient_profile p JOIN patient_relationship r ON r.patient_id=p.id
		 WHERE p.id=$1`, patient.ID).Scan(&patientStatus, &relationshipStatus))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM consent WHERE patient_id=$1`, patient.ID).Scan(&consentCount))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE resource_type='patient_relationship' AND action='patient_onboarding_accepted'`,
	).Scan(&onboardingAuditCount))
	assert.Equal(t, "active", patientStatus)
	assert.Equal(t, "active", relationshipStatus)
	assert.Equal(t, len(acceptedDocumentIDs), consentCount)
	assert.Equal(t, 1, onboardingAuditCount)

	resp = doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/sessions", token, map[string]any{
		"patientId": patient.ID, "occurredAt": "2026-07-28T10:00:00Z", "notes": "Evolução clínica",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("sessão pós-consentimento: status=%d body=%s", resp.StatusCode, body)
	}
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var createdSession struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&createdSession))
	require.NoError(t, resp.Body.Close())

	resp = doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/sessions", token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var sessions []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sessions))
	require.NoError(t, resp.Body.Close())
	assert.Len(t, sessions, 1)
	resp = doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/sessions/"+createdSession.ID.String(), token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var sessionDetail struct {
		Notes string `json:"notes"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sessionDetail))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "Evolução clínica", sessionDetail.Notes)

	resp = doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/activities", token, map[string]any{
		"templateId": templateID, "patientId": patient.ID,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var assignment struct {
		ID    uuid.UUID `json:"id"`
		Title string    `json:"title"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&assignment))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "Registro diário", assignment.Title)

	resp = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/activities?patientId="+patient.ID.String(), token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var activities []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&activities))
	require.NoError(t, resp.Body.Close())
	assert.Len(t, activities, 1)

	_, err = pool.Exec(ctx, `UPDATE activity_assignment SET status='submitted' WHERE id=$1`, assignment.ID)
	require.NoError(t, err)
	resp = doJSON(t, srv.Client(), http.MethodPatch, srv.URL+"/activities/"+assignment.ID.String(), token,
		map[string]any{"status": "reviewed"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var reviewed struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reviewed))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "reviewed", reviewed.Status)

	var otherUserID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (organization_id, email, password_hash, role)
		 VALUES ($1,'other@org.dev','x','psychologist') RETURNING id`, orgID).Scan(&otherUserID))
	_, err = pool.Exec(ctx,
		`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
		 VALUES ($1,'Dra. Outra','3','SP')`, otherUserID)
	require.NoError(t, err)
	otherToken, err := auth.GenerateToken("secret", otherUserID.String(), "psychologist", orgID.String())
	require.NoError(t, err)
	resp = doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/sessions/"+createdSession.ID.String(), otherToken, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
	resp = doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/activities/"+assignment.ID.String(), otherToken, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	patientToken, err := auth.GenerateToken("secret", uuid.NewString(), "patient", orgID.String())
	require.NoError(t, err)
	resp = doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/sessions", patientToken, nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	_, err = pool.Exec(ctx, `CREATE ROLE acolhe_rls_test NOLOGIN`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `GRANT USAGE ON SCHEMA public TO acolhe_rls_test`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`GRANT SELECT ON session, clinical_record, patient_profile, patient_relationship TO acolhe_rls_test`)
	require.NoError(t, err)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	_, err = tx.Exec(ctx, `SET LOCAL ROLE acolhe_rls_test`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('acolhe.user_role', 'psychologist', true),
		set_config('acolhe.psychologist_id', $1, true), set_config('acolhe.organization_id', $2, true)`,
		psyID.String(), orgID.String())
	require.NoError(t, err)
	var visibleSessions, visibleRecords int
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM session WHERE id=$1`, createdSession.ID).Scan(&visibleSessions))
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM clinical_record WHERE session_id=$1`, createdSession.ID).Scan(&visibleRecords))
	assert.Equal(t, 1, visibleSessions)
	assert.Equal(t, 1, visibleRecords)
}

func TestRLSMetadataAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)

	rlsTables := []string{
		"appointment",
		"clinical_record",
		"documentary_record",
		"patient_profile",
		"session",
	}
	var enabledTables int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_class
		WHERE relnamespace = 'public'::regnamespace
		  AND relname = ANY($1::text[])
		  AND relrowsecurity`, rlsTables).Scan(&enabledTables))
	assert.Equal(t, len(rlsTables), enabledTables, "todas as tabelas clínicas declaradas devem ter RLS")

	var tablesWithPolicies int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(DISTINCT tablename)
		FROM pg_policies
		WHERE schemaname = 'public'
		  AND tablename = ANY($1::text[])`, rlsTables).Scan(&tablesWithPolicies))
	assert.Equal(t, len(rlsTables), tablesWithPolicies, "RLS sem policy nega o domínio inteiro")

	type tenantFixture struct {
		orgID     uuid.UUID
		userID    uuid.UUID
		psyID     uuid.UUID
		patientID uuid.UUID
		sessionID uuid.UUID
	}
	createTenant := func(slug string) tenantFixture {
		t.Helper()
		var fixture tenantFixture
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO organization (name, slug) VALUES ($1, $2) RETURNING id`,
			"Organization "+slug, slug).Scan(&fixture.orgID))
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO "user" (organization_id, email, password_hash, role)
			 VALUES ($1, $2, 'x', 'psychologist') RETURNING id`,
			fixture.orgID, slug+"@example.test").Scan(&fixture.userID))
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
			 VALUES ($1, $2, $3, 'CE') RETURNING id`,
			fixture.userID, "Psychologist "+slug, slug).Scan(&fixture.psyID))
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO patient_profile (organization_id, full_name)
			 VALUES ($1, $2) RETURNING id`,
			fixture.orgID, "Patient "+slug).Scan(&fixture.patientID))
		_, err := pool.Exec(ctx,
			`INSERT INTO patient_relationship (
			   patient_id, psychologist_id, status, requires_health_consent
			 ) VALUES ($1, $2, 'active', false)`,
			fixture.patientID, fixture.psyID)
		require.NoError(t, err)
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO session (patient_id, psychologist_id, occurred_at)
			 VALUES ($1, $2, now()) RETURNING id`,
			fixture.patientID, fixture.psyID).Scan(&fixture.sessionID))
		return fixture
	}

	tenantA := createTenant("tenant-a")
	tenantB := createTenant("tenant-b")

	_, err := pool.Exec(ctx, `CREATE ROLE acolhe_rls_test NOLOGIN NOSUPERUSER NOBYPASSRLS`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `GRANT USAGE ON SCHEMA public TO acolhe_rls_test`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`GRANT SELECT ON patient_profile, patient_relationship, session TO acolhe_rls_test`)
	require.NoError(t, err)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	_, err = tx.Exec(ctx, `SET LOCAL ROLE acolhe_rls_test`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		SELECT set_config('acolhe.user_id', $1, true),
		       set_config('acolhe.user_role', 'psychologist', true),
		       set_config('acolhe.psychologist_id', $2, true),
		       set_config('acolhe.organization_id', $3, true)`,
		tenantA.userID.String(), tenantA.psyID.String(), tenantA.orgID.String())
	require.NoError(t, err)

	var ownPatients, otherPatients, ownSessions, otherSessions int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM patient_profile WHERE id=$1`, tenantA.patientID).Scan(&ownPatients))
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM patient_profile WHERE id=$1`, tenantB.patientID).Scan(&otherPatients))
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM session WHERE id=$1`, tenantA.sessionID).Scan(&ownSessions))
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM session WHERE id=$1`, tenantB.sessionID).Scan(&otherSessions))

	assert.Equal(t, 1, ownPatients)
	assert.Zero(t, otherPatients, "RLS não pode expor paciente de outro tenant")
	assert.Equal(t, 1, ownSessions)
	assert.Zero(t, otherSessions, "RLS não pode expor sessão de outro tenant")
}
