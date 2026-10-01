//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joycesilva/acolhe-api/internal/app"
	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/patient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDailyCheckinWorkflow_FullStack(t *testing.T) {
	ctx := context.Background()
	pool := workflowPool(t)
	var orgID, psyUserID, psyID, patientID, patientUserID, otherPatientID, otherPatientUserID, documentID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO organization (name, slug) VALUES ('Workflow', 'workflow') RETURNING id`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO "user" (organization_id, email, password_hash, role) VALUES ($1, 'psy@workflow.test', 'x', 'psychologist') RETURNING id`, orgID).Scan(&psyUserID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state) VALUES ($1, 'Psicóloga', '123', 'SP') RETURNING id`, psyUserID).Scan(&psyID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO consent_document (scope, version, title, content, content_sha256, published_at) VALUES ('health_data', 'workflow', 'Dados', 'Conteúdo', repeat('a',64), now()) RETURNING id`).Scan(&documentID))
	for i := 0; i < 2; i++ {
		var userID, patient, consentID uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO "user" (organization_id, email, password_hash, role) VALUES ($1, $2, 'x', 'patient') RETURNING id`, orgID, fmt.Sprintf("patient%d@workflow.test", i)).Scan(&userID))
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO patient_profile (organization_id, user_id, full_name, status) VALUES ($1,$2,$3,'active') RETURNING id`, orgID, userID, fmt.Sprintf("Paciente %d", i)).Scan(&patient))
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO consent (user_id, patient_id, document_id, accepted) VALUES ($1,$2,$3,true) RETURNING id`, userID, patient, documentID).Scan(&consentID))
		_, err := pool.Exec(ctx, `INSERT INTO patient_relationship (patient_id, psychologist_id, consent_id, status) VALUES ($1,$2,$3,'active')`, patient, psyID, consentID)
		require.NoError(t, err)
		if i == 0 {
			patientID, patientUserID = patient, userID
		} else {
			otherPatientID, otherPatientUserID = patient, userID
		}
	}
	// Executa com RLS real, sem superusuário ou BYPASSRLS.
	role := "workflow_" + uuid.New().String()[:8]
	_, err := pool.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN NOSUPERUSER NOBYPASSRLS")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "GRANT USAGE ON SCHEMA public TO "+pgx.Identifier{role}.Sanitize())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "+pgx.Identifier{role}.Sanitize())
	require.NoError(t, err)
	cfg := pool.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	rlsPool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(rlsPool.Close)
	srv := httptest.NewServer(app.New(rlsPool, db.New(rlsPool), nil, "secret").Handler())
	t.Cleanup(srv.Close)
	psyToken, err := auth.GenerateToken("secret", psyUserID.String(), "psychologist", orgID.String())
	require.NoError(t, err)
	patientToken, err := auth.GenerateToken("secret", patientUserID.String(), "patient", orgID.String())
	require.NoError(t, err)
	otherPatientToken, err := auth.GenerateToken("secret", otherPatientUserID.String(), "patient", orgID.String())
	require.NoError(t, err)
	call := func(method, path, token string, body any, status int, output any) {
		t.Helper()
		response := doJSON(t, srv.Client(), method, srv.URL+path, token, body)
		defer func() { _ = response.Body.Close() }()
		var raw json.RawMessage
		require.NoError(t, json.NewDecoder(response.Body).Decode(&raw))
		require.Equal(t, status, response.StatusCode, "%s %s: %s", method, path, raw)
		if output != nil {
			require.NoError(t, json.Unmarshal(raw, output))
		}
	}

	var saved patient.PatientCheckin
	call("POST", "/patient/check-ins", patientToken, map[string]any{"mood": 4, "note": "  Hoje estou bem  ", "patientId": otherPatientID}, 201, &saved)
	assert.Equal(t, "Hoje estou bem", *saved.Note)
	assert.Equal(t, time.Now().In(time.FixedZone("Fortaleza", -3*60*60)).Format("2006-01-02"), saved.Day)
	originalID, originalDate := saved.ID, saved.CreatedAt
	call("POST", "/patient/check-ins", patientToken, map[string]any{"mood": 1}, 409, nil)
	call("POST", "/checkins", psyToken, map[string]any{"patientId": patientID, "mood": 1}, 409, nil)
	var history []patient.PatientCheckin
	call("GET", "/patient/check-ins", patientToken, nil, 200, &history)
	require.Len(t, history, 1)
	assert.Equal(t, int32(4), history[0].Mood)
	call("GET", "/checkins?patientId="+patientID.String(), psyToken, nil, 200, &history)
	require.Len(t, history, 1)
	assert.Equal(t, originalID, history[0].ID)
	assert.Equal(t, "Hoje estou bem", *history[0].Note)
	call("GET", "/patient/check-ins?patientId="+patientID.String(), otherPatientToken, nil, 200, &history)
	assert.Empty(t, history)
	editPath := "/patient/check-ins/" + originalID.String()
	call("PUT", editPath, otherPatientToken, map[string]any{"mood": 2}, 409, nil)
	call("PUT", editPath, psyToken, map[string]any{"mood": 2}, 403, nil)
	call("PUT", editPath, patientToken, map[string]any{"mood": 5, "note": "Melhorei"}, 200, &saved)
	assert.Equal(t, originalID, saved.ID)
	assert.True(t, originalDate.Equal(saved.CreatedAt))
	assert.Equal(t, int32(5), saved.Mood)
	assert.Equal(t, "Melhorei", *saved.Note)
	call("GET", "/checkins?patientId="+patientID.String(), psyToken, nil, 200, &history)
	require.Len(t, history, 1)
	assert.Equal(t, int32(5), history[0].Mood)
	// Ontem permanece no histórico, mas não pode ser editado hoje.
	var yesterdayID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO checkin (patient_id, mood, note, created_at, updated_at, daily_day)
  VALUES ($1, 2, 'Ontem', now() - interval '1 day', now() - interval '1 day', (now() AT TIME ZONE 'America/Fortaleza')::date - 1) RETURNING id`, patientID).Scan(&yesterdayID))
	call("PUT", "/patient/check-ins/"+yesterdayID.String(), patientToken, map[string]any{"mood": 3}, 409, nil)
	call("GET", "/patient/check-ins", patientToken, nil, 200, &history)
	require.Len(t, history, 2)
	assert.Equal(t, originalID, history[0].ID)
	var summary patient.ProcessSummary
	call("GET", "/patient/process-summary", patientToken, nil, 200, &summary)
	assert.Equal(t, int32(2), summary.CheckinCount)
	// Psicólogo da mesma organização sem vínculo não vê os check-ins.
	var unrelatedUserID, unrelatedPsyID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO "user" (organization_id,email,password_hash,role) VALUES ($1,'unrelated@workflow.test','x','psychologist') RETURNING id`, orgID).Scan(&unrelatedUserID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO psychologist_profile (user_id,full_name,crp_number,crp_state) VALUES ($1,'Sem vínculo','456','SP') RETURNING id`, unrelatedUserID).Scan(&unrelatedPsyID))
	unrelatedToken, err := auth.GenerateToken("secret", unrelatedUserID.String(), "psychologist", orgID.String())
	require.NoError(t, err)
	call("GET", "/checkins?patientId="+patientID.String(), unrelatedToken, nil, 200, &history)
	assert.Empty(t, history)
	// Duas abas simultâneas não criam dois registros diários.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := doJSON(t, srv.Client(), "POST", srv.URL+"/patient/check-ins", otherPatientToken, map[string]any{"mood": 3})
			codes <- response.StatusCode
			_ = response.Body.Close()
		}()
	}
	wg.Wait()
	close(codes)
	var statuses []int
	for code := range codes {
		statuses = append(statuses, code)
	}
	assert.ElementsMatch(t, []int{201, 409}, statuses)
	call("PUT", editPath, patientToken, map[string]any{"mood": 0}, 400, nil)
	call("PUT", editPath, patientToken, map[string]any{"mood": 4, "note": string(make([]byte, 1001))}, 400, nil)
	call("PUT", editPath, patientToken, map[string]any{"mood": 5, "note": "  "}, 200, &saved)
	assert.Nil(t, saved.Note)
}

func TestDailyCheckinMigrationPreservesLegacyHistory(t *testing.T) {
	pool := workflowPool(t)
	ctx := context.Background()
	// Emula o schema anterior à migration nova sem tocar em qualquer banco do usuário.
	_, err := pool.Exec(ctx, `DROP POLICY checkin_patient_update ON checkin;
  DROP POLICY checkin_clinical_select ON checkin; DROP POLICY checkin_clinical_insert ON checkin;
  ALTER TABLE checkin DROP COLUMN daily_day; ALTER TABLE checkin DROP COLUMN updated_at;`)
	require.NoError(t, err)
	var orgID, patientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO organization(name,slug) VALUES ('History','history') RETURNING id`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO patient_profile(organization_id,full_name) VALUES ($1,'Histórico') RETURNING id`, orgID).Scan(&patientID))
	_, err = pool.Exec(ctx, `INSERT INTO checkin(patient_id,mood,note,created_at) VALUES
 ($1,1,'Antes da meia-noite','2026-09-29T02:59:00Z'),
 ($1,2,'Após a meia-noite','2026-09-29T03:01:00Z'),
 ($1,3,'Duplicado antigo','2026-09-29T15:00:00Z'),
 ($1,4,'Mais recente','2026-09-29T18:00:00Z')`, patientID)
	require.NoError(t, err)
	_, file, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "db", "migrations", "20261001130000_daily_checkin.sql"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(migration))
	require.NoError(t, err)
	var total, canonical int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*),count(daily_day) FROM checkin WHERE patient_id=$1`, patientID).Scan(&total, &canonical))
	assert.Equal(t, 4, total)
	assert.Equal(t, 2, canonical)
	var firstDay, secondDay string
	require.NoError(t, pool.QueryRow(ctx, `SELECT daily_day::text FROM checkin WHERE note='Antes da meia-noite'`).Scan(&firstDay))
	require.NoError(t, pool.QueryRow(ctx, `SELECT daily_day::text FROM checkin WHERE note='Mais recente'`).Scan(&secondDay))
	assert.Equal(t, "2026-09-28", firstDay)
	assert.Equal(t, "2026-09-29", secondDay)
	_, err = pool.Exec(ctx, `INSERT INTO checkin(patient_id,mood,created_at,daily_day) VALUES ($1,5,'2026-09-29T20:00:00Z','2026-09-29')`, patientID)
	var dbError *pgconn.PgError
	require.ErrorAs(t, err, &dbError)
	assert.Equal(t, "23505", dbError.Code)
}
