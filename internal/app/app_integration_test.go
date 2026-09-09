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
	"github.com/jackc/pgx/v5"
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
	fieldFixtures := []struct {
		code, label, fieldType, config string
		order                          int
	}{
		{"situation", "Descreva a situação", "long_text", `{"maxLength":2000}`, 1},
		{"intensity", "Intensidade da emoção", "scale", `{"min":1,"max":10}`, 2},
		{"recognized", "Reconheceu a distorção?", "boolean", `{}`, 3},
		{"observed_at", "Quando percebeu?", "datetime", `{}`, 4},
		{"distortions", "Distorções reconhecidas", "multiple_choice", `{"options":["Catastrofização","Leitura mental"]}`, 5},
	}
	fieldIDs := make([]uuid.UUID, len(fieldFixtures))
	for index, fixture := range fieldFixtures {
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO activity_field (
			   template_id, code, label, field_type, config, display_order
			 ) VALUES ($1,$2,$3,$4,$5::jsonb,$6) RETURNING id`,
			templateID, fixture.code, fixture.label, fixture.fieldType,
			fixture.config, fixture.order).Scan(&fieldIDs[index]))
	}

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

	// Public invitation reads run without tenant claims. Exercise the endpoint
	// through a production-like role that cannot bypass patient_profile RLS.
	_, err = pool.Exec(ctx, `CREATE ROLE acolhe_invitation_test NOLOGIN NOSUPERUSER NOBYPASSRLS`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `GRANT USAGE ON SCHEMA public TO acolhe_invitation_test`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `GRANT SELECT ON consent_document TO acolhe_invitation_test`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION get_patient_invitation(bytea) TO acolhe_invitation_test`)
	require.NoError(t, err)
	rlsConfig, err := pgxpool.ParseConfig(pool.Config().ConnString())
	require.NoError(t, err)
	rlsConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE acolhe_invitation_test`)
		return err
	}
	rlsPool, err := pgxpool.NewWithConfig(ctx, rlsConfig)
	require.NoError(t, err)
	t.Cleanup(rlsPool.Close)
	rlsServer := httptest.NewServer(app.New(rlsPool, db.New(rlsPool), nil, "secret").Handler())
	t.Cleanup(rlsServer.Close)
	rlsResponse := doJSON(t, rlsServer.Client(), http.MethodGet,
		rlsServer.URL+"/onboarding/invitations/"+patient.Invitation.Token, "", nil)
	require.Equal(t, http.StatusOK, rlsResponse.StatusCode)
	require.NoError(t, rlsResponse.Body.Close())

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

	var responseID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO activity_response (assignment_id, submitted_at, is_draft)
		 VALUES ($1, '2026-07-28T21:12:00Z', false) RETURNING id`,
		assignment.ID).Scan(&responseID))
	valueStatements := []struct {
		query string
		value any
	}{
		{`INSERT INTO activity_response_value
		   (response_id, field_id, field_code, value_text)
		   VALUES ($1,$2,$3,$4)`, "Reunião com gestor sobre os números do trimestre."},
		{`INSERT INTO activity_response_value
		   (response_id, field_id, field_code, value_number)
		   VALUES ($1,$2,$3,$4)`, 7},
		{`INSERT INTO activity_response_value
		   (response_id, field_id, field_code, value_boolean)
		   VALUES ($1,$2,$3,$4)`, true},
		{`INSERT INTO activity_response_value
		   (response_id, field_id, field_code, value_datetime)
		   VALUES ($1,$2,$3,$4)`, time.Date(2026, 7, 28, 18, 30, 0, 0, time.UTC)},
		{`INSERT INTO activity_response_value
		   (response_id, field_id, field_code, value_json)
		   VALUES ($1,$2,$3,$4::jsonb)`, `["Catastrofização","Leitura mental"]`},
	}
	for index, value := range valueStatements {
		_, err = pool.Exec(ctx, value.query,
			responseID, fieldIDs[index], fieldFixtures[index].code, value.value)
		require.NoError(t, err)
	}
	_, err = pool.Exec(ctx, `UPDATE activity_assignment SET status='submitted' WHERE id=$1`, assignment.ID)
	require.NoError(t, err)

	type reviewDetail struct {
		State           string     `json:"state"`
		Status          string     `json:"status"`
		TemplateVersion int32      `json:"templateVersion"`
		FieldCount      int32      `json:"fieldCount"`
		ReviewedAt      *time.Time `json:"reviewedAt"`
		Submission      *struct {
			ID          uuid.UUID `json:"id"`
			SubmittedAt time.Time `json:"submittedAt"`
			Fields      []struct {
				Code         string          `json:"code"`
				Kind         string          `json:"kind"`
				DisplayOrder int32           `json:"displayOrder"`
				Value        json.RawMessage `json:"value"`
			} `json:"fields"`
		} `json:"submission"`
	}
	resp = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/activities/"+assignment.ID.String(), token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var submitted reviewDetail
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&submitted))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "submitted", submitted.State)
	assert.Equal(t, int32(1), submitted.TemplateVersion)
	assert.Equal(t, int32(len(fieldFixtures)), submitted.FieldCount)
	require.NotNil(t, submitted.Submission)
	assert.Equal(t, responseID, submitted.Submission.ID)
	require.Len(t, submitted.Submission.Fields, len(fieldFixtures))
	assert.Equal(t, []string{"text", "number", "boolean", "datetime", "json"}, []string{
		submitted.Submission.Fields[0].Kind,
		submitted.Submission.Fields[1].Kind,
		submitted.Submission.Fields[2].Kind,
		submitted.Submission.Fields[3].Kind,
		submitted.Submission.Fields[4].Kind,
	})
	for index, field := range submitted.Submission.Fields {
		assert.Equal(t, int32(index+1), field.DisplayOrder)
	}

	resp = doJSON(t, srv.Client(), http.MethodPut,
		srv.URL+"/activities/"+assignment.ID.String()+"/review", token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var reviewed reviewDetail
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reviewed))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "reviewed", reviewed.State)
	assert.Equal(t, "reviewed", reviewed.Status)
	require.NotNil(t, reviewed.ReviewedAt)
	firstReviewedAt := *reviewed.ReviewedAt

	resp = doJSON(t, srv.Client(), http.MethodPut,
		srv.URL+"/activities/"+assignment.ID.String()+"/review", token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reviewed))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, firstReviewedAt, *reviewed.ReviewedAt, "replay deve preservar o primeiro reviewedAt")

	// Missing and legacy-incomplete submissions fail closed.
	resp = doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/activities", token, map[string]any{
		"templateId": templateID, "patientId": patient.ID,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var incompleteAssignment struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&incompleteAssignment))
	require.NoError(t, resp.Body.Close())
	resp = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/activities/"+incompleteAssignment.ID.String(), token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var awaiting reviewDetail
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&awaiting))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "awaiting_response", awaiting.State)
	assert.Nil(t, awaiting.Submission)
	resp = doJSON(t, srv.Client(), http.MethodPut,
		srv.URL+"/activities/"+incompleteAssignment.ID.String()+"/review", token, nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	_, err = pool.Exec(ctx,
		`INSERT INTO activity_response (assignment_id, submitted_at, is_draft)
		 VALUES ($1, now(), false)`, incompleteAssignment.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`UPDATE activity_assignment SET status='submitted' WHERE id=$1`, incompleteAssignment.ID)
	require.NoError(t, err)
	resp = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/activities/"+incompleteAssignment.ID.String(), token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var invalid reviewDetail
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&invalid))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "submission_invalid", invalid.State)
	assert.Nil(t, invalid.Submission)

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
