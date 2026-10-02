//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joycesilva/acolhe-api/internal/app"
	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/documentary"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestDocumentaryWorkflow_FullStack(t *testing.T) {
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
	keys, err := documentary.ParseKeyring("v1", `{"v1":"`+strings.Repeat("ab", 32)+`","v2":"`+strings.Repeat("cd", 32)+`"}`)
	require.NoError(t, err)
	srv := httptest.NewServer(app.New(rlsPool, db.New(rlsPool), nil, "secret", app.WithDocumentaryKeys(keys)).Handler())
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

	_ = otherPatientID
	_ = otherPatientToken
	path := "/documentary/patients/" + patientID.String()
	var notebooks documentary.PatientNotebooks
	call("GET", path, psyToken, nil, 200, &notebooks)
	assert.True(t, notebooks.Patient.Writable)
	assert.Empty(t, notebooks.Items)
	var patients documentary.Page[documentary.Patient]
	call("GET", "/documentary/patients", psyToken, nil, 200, &patients)
	assert.Zero(t, patients.TotalPages)
	var saved documentary.Notebook
	save := func(text string, revision int, status int) {
		t.Helper()
		if status == 200 {
			call("PUT", path+"/hypothesis", psyToken, map[string]any{"content": text, "expectedRevision": revision}, status, &saved)
		} else {
			call("PUT", path+"/hypothesis", psyToken, map[string]any{"content": text, "expectedRevision": revision}, status, nil)
		}
	}
	text := "  Hipótese 📝\n segunda linha  "
	save(text, 0, 200)
	assert.Equal(t, text, saved.Content)
	assert.Equal(t, 1, saved.Revision)
	original := saved
	save(text, 0, 200)
	assert.Equal(t, 1, saved.Revision)
	save("alteração", 0, 409)
	save("", 1, 200)
	assert.Equal(t, 2, saved.Revision)
	historyPath := "/documentary/notebooks/" + saved.ID.String() + "/versions"
	var history documentary.Page[documentary.Version]
	call("GET", historyPath+"?pageSize=1", psyToken, nil, 200, &history)
	assert.Equal(t, 2, history.TotalCount)
	assert.Equal(t, 2, history.TotalPages)
	require.Len(t, history.Items, 1)
	assert.Equal(t, 2, history.Items[0].Revision)
	var version documentary.Version
	call("GET", historyPath+"/1", psyToken, nil, 200, &version)
	assert.Equal(t, text, *version.Content)
	call("POST", path+"/hypothesis/restore", psyToken, map[string]any{"revision": 1, "expectedRevision": 2}, 200, &saved)
	assert.Equal(t, 3, saved.Revision)
	assert.Equal(t, text, saved.Content)
	call("GET", historyPath, psyToken, nil, 200, &history)
	assert.Equal(t, 1, *history.Items[0].RestoredFrom)
	call("GET", "/documentary/patients?pageSize=1", psyToken, nil, 200, &patients)
	assert.Equal(t, 1, patients.TotalCount)
	call("GET", "/documentary/patients?page=0", psyToken, nil, 400, nil)
	call("GET", "/documentary/patients?pageSize=101", psyToken, nil, 400, nil)
	call("GET", path, patientToken, nil, 403, nil)
	adminToken, e := auth.GenerateToken("secret", psyUserID.String(), "org_admin", orgID.String())
	require.NoError(t, e)
	call("GET", historyPath, adminToken, nil, 403, nil)
	var secondUser, secondPsych uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO "user"(organization_id,email,password_hash,role) VALUES($1,'second@workflow.test','x','psychologist') RETURNING id`, orgID).Scan(&secondUser))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO psychologist_profile(user_id,full_name,crp_number,crp_state) VALUES($1,'Outra','321','SP') RETURNING id`, secondUser).Scan(&secondPsych))
	_, err = pool.Exec(ctx, `INSERT INTO patient_relationship(patient_id,psychologist_id,status,requires_health_consent) VALUES($1,$2,'paused',false)`, patientID, secondPsych)
	require.NoError(t, err)
	secondToken, e := auth.GenerateToken("secret", secondUser.String(), "psychologist", orgID.String())
	require.NoError(t, e)
	call("GET", historyPath, secondToken, nil, 404, nil)
	call("GET", path, secondToken, nil, 200, &notebooks)
	assert.Empty(t, notebooks.Items)
	for _, category := range []string{"planning", "hypothesis"} {
		expected := 0
		if category == "hypothesis" {
			expected = 3
		}
		var wg sync.WaitGroup
		codes := make(chan int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				r := doJSON(t, srv.Client(), "PUT", srv.URL+path+"/"+category, psyToken, map[string]any{"content": fmt.Sprintf("texto %d", i), "expectedRevision": expected})
				codes <- r.StatusCode
				_ = r.Body.Close()
			}(i)
		}
		wg.Wait()
		close(codes)
		var got []int
		for c := range codes {
			got = append(got, c)
		}
		assert.ElementsMatch(t, []int{200, 409}, got)
	}
	_, err = pool.Exec(ctx, `UPDATE patient_relationship SET status='paused' WHERE patient_id=$1 AND psychologist_id=$2`, patientID, psyID)
	require.NoError(t, err)
	call("GET", path, psyToken, nil, 200, &notebooks)
	assert.False(t, notebooks.Patient.Writable)
	save("bloqueado", 4, 403)
	call("POST", path+"/hypothesis/restore", psyToken, map[string]any{"revision": 1, "expectedRevision": 4}, 403, nil)
	call("GET", historyPath+"/1", psyToken, nil, 200, &version)
	// Inactive patient also keeps history while blocking writes, independently of relationship.
	_, err = pool.Exec(ctx, `UPDATE patient_relationship SET status='active' WHERE patient_id=$1 AND psychologist_id=$2`, patientID, psyID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE patient_profile SET status='archived' WHERE id=$1`, patientID)
	require.NoError(t, err)
	call("GET", path, psyToken, nil, 200, &notebooks)
	assert.False(t, notebooks.Patient.Writable)
	save("blocked archived", 4, 403)
	call("GET", historyPath+"/1", psyToken, nil, 200, &version)
	_, err = pool.Exec(ctx, `UPDATE patient_profile SET status='active' WHERE id=$1`, patientID)
	require.NoError(t, err)
	var encrypted []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT content_encrypted FROM documentary_record_version WHERE record_id=$1 AND revision=1`, original.ID).Scan(&encrypted))
	assert.NotContains(t, string(encrypted), text)
	inv, e := documentary.InventoryContents(ctx, pool, keys)
	require.NoError(t, e)
	assert.Zero(t, inv.Failures)
	assert.Equal(t, 2, inv.Current["v1"])
	newKeys, e := documentary.ParseKeyring("v2", `{"v1":"`+strings.Repeat("ab", 32)+`","v2":"`+strings.Repeat("cd", 32)+`"}`)
	require.NoError(t, e)
	rotated, e := documentary.Reencrypt(ctx, pool, newKeys, "v1", "v2", 1, 1)
	require.Error(t, e)
	assert.Equal(t, 2, rotated.Migrated)
	rotated, e = documentary.Reencrypt(ctx, pool, newKeys, "v1", "v2", 1, 100)
	require.NoError(t, e)
	assert.Zero(t, rotated.Inventory.Failures)
	assert.Zero(t, rotated.Inventory.Versions["v1"])
	call("GET", historyPath+"/1", psyToken, nil, 200, &version)
	assert.Equal(t, text, *version.Content)
	assert.Equal(t, 1, version.Revision)
	// RLS itself protects both tables, even without the application's author predicate.
	for _, identity := range []tenant.Identity{
		{UserID: secondUser, OrgID: orgID, Role: "psychologist"},
		{UserID: psyUserID, OrgID: orgID, Role: "org_admin"},
		{UserID: patientUserID, OrgID: orgID, Role: "patient"},
	} {
		tx, _, e := tenant.BeginTransaction(ctx, rlsPool, identity)
		require.NoError(t, e)
		var visible int
		require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM documentary_record WHERE id=$1`, original.ID).Scan(&visible))
		assert.Zero(t, visible)
		require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM documentary_record_version WHERE record_id=$1`, original.ID).Scan(&visible))
		assert.Zero(t, visible)
		require.NoError(t, tx.Rollback(ctx))
	}
	_, e = documentary.InventoryContents(ctx, rlsPool, newKeys)
	require.Error(t, e)
	// Rotation and a writer using the new active key may safely overlap.
	authorCtx := tenant.WithIdentity(ctx, tenant.Identity{UserID: psyUserID, OrgID: orgID, Role: "psychologist"})
	oldWriter := documentary.NewService(rlsPool, keys)
	_, e = oldWriter.Save(authorCtx, patientID, "other", "before rotation", 0, nil)
	require.NoError(t, e)
	newWriter := documentary.NewService(rlsPool, newKeys)
	writerDone := make(chan error, 1)
	go func() {
		for revision := 1; revision <= 20; revision++ {
			_, writeErr := newWriter.Save(authorCtx, patientID, "other", fmt.Sprintf("concurrent %d", revision), revision, nil)
			if writeErr != nil {
				writerDone <- writeErr
				return
			}
		}
		writerDone <- nil
	}()
	_, _ = documentary.Reencrypt(ctx, pool, newKeys, "v1", "v2", 1, 100)
	require.NoError(t, <-writerDone)
	_, e = documentary.Reencrypt(ctx, pool, newKeys, "v1", "v2", 1, 100)
	require.NoError(t, e)
	onlyNew, e := documentary.ParseKeyring("v2", `{"v2":"`+strings.Repeat("cd", 32)+`"}`)
	require.NoError(t, e)
	recovered, e := documentary.NewService(rlsPool, onlyNew).Notebooks(authorCtx, patientID)
	require.NoError(t, e)
	for _, n := range recovered.Items {
		if n.Category == "other" {
			assert.Equal(t, 21, n.Revision)
			assert.Equal(t, "concurrent 20", n.Content)
		}
	}
	oldVersion, e := documentary.NewService(rlsPool, onlyNew).GetVersion(authorCtx, original.ID, 1)
	require.NoError(t, e)
	assert.Equal(t, text, *oldVersion.Content)
	call("GET", historyPath+"?page=100&pageSize=1", psyToken, nil, 200, &history)
	assert.Empty(t, history.Items)
	assert.Positive(t, history.TotalCount)

	_, err = pool.Exec(ctx, `UPDATE documentary_record SET content_encrypted='invalid'::bytea WHERE id=$1`, original.ID)
	require.NoError(t, err)
	call("GET", path, psyToken, nil, 503, nil)
	save("cannot overwrite corrupt content", 4, 503)
}

func TestDocumentaryMigrationPreservesLegacy(t *testing.T) {
	ctx := context.Background()
	pool := workflowPool(t)
	_, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	require.NoError(t, err)
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "db", "migrations")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	const target = "20261001160000_documentary_notebooks.sql"
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= target {
			continue
		}
		sql, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, e)
		_, e = pool.Exec(ctx, string(sql))
		require.NoError(t, e, entry.Name())
	}
	var org, user, author, patient uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO organization(name,slug) VALUES('Legacy','legacy') RETURNING id`).Scan(&org))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO "user"(organization_id,email,password_hash,role) VALUES($1,'legacy@test.local','x','psychologist') RETURNING id`, org).Scan(&user))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO psychologist_profile(user_id,full_name,crp_number,crp_state) VALUES($1,'Legacy','123','SP') RETURNING id`, user).Scan(&author))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO patient_profile(organization_id,full_name) VALUES($1,'Legacy') RETURNING id`, org).Scan(&patient))
	_, err = pool.Exec(ctx, `INSERT INTO documentary_record(author_id,patient_id,category,content_encrypted,tags) VALUES ($1,NULL,'other','legacy-1',ARRAY['tag']),($1,$2,'planning','legacy-2',NULL),($1,$2,'planning','legacy-3',NULL)`, author, patient)
	require.NoError(t, err)
	migration, err := os.ReadFile(filepath.Join(dir, target))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(migration))
	require.NoError(t, err)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM documentary_record_legacy WHERE author_id=$1 AND content_encrypted IN ('legacy-1'::bytea,'legacy-2'::bytea,'legacy-3'::bytea)`, author).Scan(&count))
	assert.Equal(t, 3, count)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM documentary_record`).Scan(&count))
	assert.Zero(t, count)
}
