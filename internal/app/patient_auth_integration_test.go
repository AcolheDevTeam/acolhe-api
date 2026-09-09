//go:build integration

package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/app"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

func TestPatientInvitationLoginMeAndPortalIsolation(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	var orgID, psychologistUserID, psychologistID, patientID, relationshipID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name, slug) VALUES ('Org paciente', 'org-paciente') RETURNING id`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (organization_id, email, password_hash, role)
		 VALUES ($1, 'psi@paciente.dev', 'unused', 'psychologist') RETURNING id`, orgID).Scan(&psychologistUserID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
		 VALUES ($1, 'Dra. Acolhe', '999', 'SP') RETURNING id`, psychologistUserID).Scan(&psychologistID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO patient_profile (organization_id, full_name) VALUES ($1, 'Paciente A') RETURNING id`, orgID).Scan(&patientID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO patient_relationship (patient_id, psychologist_id, status)
		 VALUES ($1, $2, 'pending') RETURNING id`, patientID, psychologistID).Scan(&relationshipID))

	const invitationToken = "patient-invitation-token"
	hash := sha256.Sum256([]byte(invitationToken))
	_, err := pool.Exec(ctx, `INSERT INTO patient_invitation
		(patient_id, relationship_id, email, token_hash, expires_at)
		VALUES ($1, $2, 'paciente@acolhe.dev', $3, $4)`, patientID, relationshipID,
		base64.RawURLEncoding.EncodeToString(hash[:]), time.Now().Add(time.Hour))
	require.NoError(t, err)

	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)

	accept := requestJSON(t, srv.Client(), http.MethodPost, srv.URL+"/invitations/"+invitationToken+"/accept",
		`{"password":"senha-segura","consentVersion":"2026-01"}`, "")
	require.Equal(t, http.StatusCreated, accept.StatusCode)
	accept.Body.Close()

	login := requestJSON(t, srv.Client(), http.MethodPost, srv.URL+"/login",
		`{"email":"paciente@acolhe.dev","password":"senha-segura"}`, "")
	require.Equal(t, http.StatusOK, login.StatusCode)
	var loginResult struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(login.Body).Decode(&loginResult))
	login.Body.Close()
	require.NotEmpty(t, loginResult.Token)

	me := requestJSON(t, srv.Client(), http.MethodGet, srv.URL+"/me", "", loginResult.Token)
	require.Equal(t, http.StatusOK, me.StatusCode)
	var meResult struct {
		Role    string `json:"role"`
		Patient struct {
			ID                 string `json:"id"`
			RelationshipStatus string `json:"relationshipStatus"`
			Consented          bool   `json:"consented"`
		} `json:"patient"`
	}
	require.NoError(t, json.NewDecoder(me.Body).Decode(&meResult))
	me.Body.Close()
	require.Equal(t, "patient", meResult.Role)
	require.Equal(t, patientID.String(), meResult.Patient.ID)
	require.Equal(t, "active", meResult.Patient.RelationshipStatus)
	require.True(t, meResult.Patient.Consented)

	contextResponse := requestJSON(t, srv.Client(), http.MethodGet, srv.URL+"/patient/context", "", loginResult.Token)
	require.Equal(t, http.StatusOK, contextResponse.StatusCode)
	contextResponse.Body.Close()

	otherPatientID := uuid.New()
	denied := requestJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/sessions?patientId="+otherPatientID.String(), "", loginResult.Token)
	require.Equal(t, http.StatusForbidden, denied.StatusCode)
	denied.Body.Close()

	_, err = pool.Exec(ctx, `UPDATE patient_relationship SET status = 'ended', ended_at = now() WHERE id = $1`, relationshipID)
	require.NoError(t, err)
	ended := requestJSON(t, srv.Client(), http.MethodGet, srv.URL+"/patient/context", "", loginResult.Token)
	require.Equal(t, http.StatusNotFound, ended.StatusCode)
	ended.Body.Close()

}

func requestJSON(t *testing.T, client *http.Client, method, url, body, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}
