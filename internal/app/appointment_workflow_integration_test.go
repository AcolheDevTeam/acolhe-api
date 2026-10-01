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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/app"
	"github.com/joycesilva/acolhe-api/internal/appointment"
	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/session"
)

// WORKFLOW_TEST_DATABASE_URL permite usar um Postgres local isolado quando Docker
// não está disponível. Cada execução cria e remove seu próprio banco.
func workflowPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("WORKFLOW_TEST_DATABASE_URL")
	if dsn == "" {
		return setupPool(t)
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	name := "workflow_" + uuid.New().String()[:8]
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	config, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		admin.Close()
	})
	_, file, _, _ := runtime.Caller(0)
	schema, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "db", "schema.sql"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(schema))
	require.NoError(t, err)
	return pool
}

func TestAppointmentWorkflow_FullStack(t *testing.T) {
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
	future := time.Now().UTC().AddDate(0, 0, 1).Truncate(time.Second)
	payload := func(date time.Time) map[string]any {
		return map[string]any{"patientId": patientID, "scheduledFor": date, "durationMinutes": 50, "modality": "online"}
	}
	var scheduled appointment.Appointment
	call("POST", "/appointments", psyToken, payload(future), 201, &scheduled)
	assert.Equal(t, patientID, scheduled.PatientID)
	var next *appointment.Appointment
	call("GET", "/patient/next-session", patientToken, nil, 200, &next)
	require.NotNil(t, next)
	assert.Equal(t, scheduled.ID, next.ID)
	call("GET", "/patient/next-session?patientId="+patientID.String(), otherPatientToken, nil, 200, &next)
	assert.Nil(t, next)
	path := "/appointments/" + scheduled.ID.String()
	confirmPath := "/patient/appointments/" + scheduled.ID.String() + "/confirm"
	call("POST", confirmPath, otherPatientToken, nil, 404, nil)
	wrongOrgToken, err := auth.GenerateToken("secret", patientUserID.String(), "patient", uuid.NewString())
	require.NoError(t, err)
	call("POST", confirmPath, wrongOrgToken, nil, 404, nil)
	call("POST", confirmPath, psyToken, nil, 403, nil)
	call("POST", "/patient/appointments/invalid/confirm", patientToken, nil, 400, nil)
	// Campos extras não permitem alterar paciente, horário ou concluir atendimento.
	call("POST", confirmPath, patientToken, map[string]any{"status": "completed", "patientId": otherPatientID}, 200, nil)
	call("POST", confirmPath, patientToken, nil, 200, nil)
	var patientConfirmed appointment.Appointment
	call("GET", path, psyToken, nil, 200, &patientConfirmed)
	assert.Equal(t, "confirmed", patientConfirmed.Status)
	assert.Equal(t, scheduled.ScheduledFor, patientConfirmed.ScheduledFor)
	assert.Equal(t, scheduled.PatientID, patientConfirmed.PatientID)
	call("PUT", path+"/status", psyToken, map[string]any{"status": "confirmed"}, 200, nil)
	call("POST", confirmPath, patientToken, nil, 200, nil)
	call("GET", "/patient/next-session", patientToken, nil, 200, &next)
	require.NotNil(t, next)
	assert.Equal(t, "confirmed", next.Status)
	call("POST", path+"/session", psyToken, nil, 409, nil)
	call("PUT", path+"/status", psyToken, map[string]any{"status": "completed"}, 409, nil)
	future = future.Add(time.Hour)
	forged := payload(future)
	forged["patientId"] = otherPatientID
	call("PUT", path, psyToken, forged, 200, &scheduled)
	assert.Equal(t, patientID, scheduled.PatientID, "reagendamento não troca paciente")
	assert.Equal(t, "scheduled", scheduled.Status, "reagendamento exige nova confirmação")
	call("GET", "/patient/next-session", patientToken, nil, 200, &next)
	assert.True(t, future.Equal(next.ScheduledFor))
	assert.Equal(t, "scheduled", next.Status)
	call("POST", confirmPath, patientToken, nil, 200, nil)
	call("GET", path, psyToken, nil, 200, &scheduled)
	assert.Equal(t, "confirmed", scheduled.Status)
	// Novo reagendamento também pode ser confirmado pela psicóloga.
	future = future.Add(time.Hour)
	call("PUT", path, psyToken, payload(future), 200, &scheduled)
	assert.Equal(t, "scheduled", scheduled.Status)
	call("PUT", path+"/status", psyToken, map[string]any{"status": "confirmed"}, 200, nil)
	call("GET", "/patient/next-session", patientToken, nil, 200, &next)
	assert.Equal(t, "confirmed", next.Status)
	assert.True(t, future.Equal(next.ScheduledFor))
	call("POST", "/appointments", psyToken, payload(future), 409, nil)
	// Reagendamento para o passado confirma, mas não conclui nem cria prontuário.
	pastReschedule := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Second)
	call("PUT", path, psyToken, payload(pastReschedule), 200, &scheduled)
	assert.Equal(t, "confirmed", scheduled.Status)
	call("GET", path, psyToken, nil, 200, &patientConfirmed)
	assert.Equal(t, "confirmed", patientConfirmed.Status)
	assert.Nil(t, patientConfirmed.SessionID)
	call("GET", "/patient/next-session", patientToken, nil, 200, &next)
	assert.Nil(t, next, "atendimento passado não aparece como próxima sessão")
	// Voltar ao futuro exige confirmação novamente.
	call("PUT", path, psyToken, payload(future), 200, &scheduled)
	assert.Equal(t, "scheduled", scheduled.Status)
	// A confirmação automática também funciona partindo de scheduled.
	call("PUT", path, psyToken, payload(pastReschedule), 200, &scheduled)
	assert.Equal(t, "confirmed", scheduled.Status)
	call("PUT", path, psyToken, payload(future), 200, &scheduled)
	assert.Equal(t, "scheduled", scheduled.Status)
	call("PUT", path+"/status", psyToken, map[string]any{"status": "canceled"}, 200, nil)
	call("POST", confirmPath, patientToken, nil, 409, nil)
	call("GET", "/patient/next-session", patientToken, nil, 200, &next)
	assert.Nil(t, next)
	call("PUT", path, psyToken, payload(future.Add(time.Hour)), 409, nil)
	// Duas criações concorrentes para o mesmo horário: só uma pode ser persistida.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := doJSON(t, srv.Client(), "POST", srv.URL+"/appointments", psyToken, payload(future))
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
	// Atendimento passado abre um prontuário vazio, sem concluir a sessão.
	past := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	call("POST", "/appointments", psyToken, payload(past), 201, &scheduled)
	call("POST", "/patient/appointments/"+scheduled.ID.String()+"/confirm", patientToken, nil, 409, nil)
	path = "/appointments/" + scheduled.ID.String()
	var record session.Session
	var recordMu sync.Mutex
	var recordIDs []uuid.UUID
	codes = make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := doJSON(t, srv.Client(), "POST", srv.URL+path+"/session", psyToken, nil)
			defer func() { _ = response.Body.Close() }()
			var result session.Session
			decodeErr := json.NewDecoder(response.Body).Decode(&result)
			assert.NoError(t, decodeErr)
			codes <- response.StatusCode
			recordMu.Lock()
			recordIDs = append(recordIDs, result.ID)
			record = result
			recordMu.Unlock()
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		require.Equal(t, 200, code)
	}
	require.Len(t, recordIDs, 2)
	assert.Equal(t, recordIDs[0], recordIDs[1], "aberturas concorrentes reutilizam a mesma sessão")
	assert.Empty(t, record.Notes)
	require.NotNil(t, record.AppointmentID)
	assert.Equal(t, scheduled.ID, *record.AppointmentID)
	firstID := record.ID
	call("POST", path+"/session", psyToken, nil, 200, &record)
	assert.Equal(t, firstID, record.ID)
	call("PUT", path, psyToken, payload(future.Add(4*time.Hour)), 409, nil)
	call("GET", path, psyToken, nil, 200, &scheduled)
	assert.Equal(t, firstID, *scheduled.SessionID)
	recordPath := "/sessions/" + record.ID.String()
	call("PUT", recordPath+"/notes", psyToken, map[string]any{"notes": "  Evolução inicial  ", "version": record.Version}, 200, &record)
	assert.Equal(t, "Evolução inicial", record.Notes)
	assert.Equal(t, int32(2), record.Version)
	call("PUT", recordPath+"/notes", psyToken, map[string]any{"notes": "texto antigo", "version": 1}, 409, nil)
	call("GET", path, psyToken, nil, 200, &scheduled)
	assert.Equal(t, "scheduled", scheduled.Status, "salvar evolução não conclui o atendimento")
	call("PUT", recordPath+"/notes", psyToken, map[string]any{"notes": "", "version": record.Version}, 200, nil)
	record = session.Session{}
	call("GET", recordPath, psyToken, nil, 200, &record)
	assert.Empty(t, record.Notes)
	var versions int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM clinical_record_version v JOIN clinical_record cr ON cr.id=v.clinical_record_id WHERE cr.session_id=$1`, record.ID).Scan(&versions))
	assert.Equal(t, 2, versions)
	call("PUT", path+"/status", psyToken, map[string]any{"status": "completed"}, 200, nil)
	call("GET", recordPath, psyToken, nil, 200, &record)
	assert.Equal(t, "completed", record.Status)
	call("POST", "/patient/appointments/"+scheduled.ID.String()+"/confirm", patientToken, nil, 409, nil)
	call("PUT", path+"/status", psyToken, map[string]any{"status": "scheduled"}, 409, nil)

	// Marcar como realizada também cria o registro vazio, mesmo sem abrir a evolução antes.
	var withoutNotes appointment.Appointment
	call("POST", "/appointments", psyToken, payload(past.Add(-2*time.Hour)), 201, &withoutNotes)
	withoutNotesPath := "/appointments/" + withoutNotes.ID.String()
	call("PUT", withoutNotesPath+"/status", psyToken, map[string]any{"status": "completed"}, 200, nil)
	var emptyRecord session.Session
	call("POST", withoutNotesPath+"/session", psyToken, nil, 200, &emptyRecord)
	assert.Empty(t, emptyRecord.Notes)
	assert.Equal(t, "completed", emptyRecord.Status)
	// Outra psicóloga e outra organização não conseguem abrir/editar os dados.
	var foreignOrg, foreignUser uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO organization (name, slug) VALUES ('Other', 'other') RETURNING id`).Scan(&foreignOrg))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO "user" (organization_id, email, password_hash, role) VALUES ($1,'other@workflow.test','x','psychologist') RETURNING id`, foreignOrg).Scan(&foreignUser))
	_, err = pool.Exec(ctx, `INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state) VALUES ($1,'Outra','789','SP')`, foreignUser)
	require.NoError(t, err)
	foreignToken, err := auth.GenerateToken("secret", foreignUser.String(), "psychologist", foreignOrg.String())
	require.NoError(t, err)
	call("GET", path, foreignToken, nil, 404, nil)
	call("POST", path+"/session", foreignToken, nil, 404, nil)
	call("PUT", path, foreignToken, payload(future), 404, nil)
	call("PUT", recordPath+"/notes", foreignToken, map[string]any{"notes": "invasão", "version": record.Version}, 404, nil)
	call("PUT", recordPath+"/notes", patientToken, map[string]any{"notes": "invasão", "version": record.Version}, 403, nil)
	_, err = pool.Exec(ctx, `UPDATE clinical_record SET locked_at=now() WHERE session_id=$1`, record.ID)
	require.NoError(t, err)
	call("PUT", recordPath+"/notes", psyToken, map[string]any{"notes": "bloqueado", "version": record.Version}, 403, nil)
}
