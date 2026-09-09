//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/app"
	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

func TestPatientInvitationLoginMeAndPortalIsolation(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	var orgID, userID, psyID, patientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO organization (name, slug) VALUES ('Org portal', 'org-portal') RETURNING id`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO "user" (organization_id, email, password_hash, role) VALUES ($1, 'psi@portal.dev', 'unused', 'psychologist') RETURNING id`, orgID).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state) VALUES ($1, 'Dra. Portal', '998', 'SP') RETURNING id`, userID).Scan(&psyID))
	_, err := pool.Exec(ctx, `INSERT INTO consent_document (scope, version, title, content, content_sha256, required, published_at) VALUES ('health_data', '2026-01', 'Consentimento', 'Conteúdo', repeat('a', 64), true, now())`)
	require.NoError(t, err)

	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)
	psyToken, err := auth.GenerateToken("secret", userID.String(), "psychologist", orgID.String())
	require.NoError(t, err)
	created := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/patients", psyToken, map[string]any{"fullName": "Paciente Portal", "email": "portal@example.test"})
	require.Equal(t, http.StatusCreated, created.StatusCode)
	var patient struct {
		ID         uuid.UUID `json:"id"`
		Invitation struct {
			Token string `json:"token"`
		} `json:"invitation"`
	}
	require.NoError(t, json.NewDecoder(created.Body).Decode(&patient))
	require.NoError(t, created.Body.Close())
	patientID = patient.ID

	invitation := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/onboarding/invitations/"+patient.Invitation.Token, "", nil)
	require.Equal(t, http.StatusOK, invitation.StatusCode)
	var invite struct {
		Documents []struct {
			ID uuid.UUID `json:"id"`
		} `json:"documents"`
	}
	require.NoError(t, json.NewDecoder(invitation.Body).Decode(&invite))
	require.NoError(t, invitation.Body.Close())
	documentIDs := make([]uuid.UUID, 0, len(invite.Documents))
	for _, document := range invite.Documents {
		documentIDs = append(documentIDs, document.ID)
	}
	require.NotEmpty(t, documentIDs)
	accepted := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/onboarding/invitations/"+patient.Invitation.Token+"/accept", "", map[string]any{"password": "senha-segura", "acceptedDocumentIds": documentIDs})
	require.Equal(t, http.StatusOK, accepted.StatusCode)
	accepted.Body.Close()

	login := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/login", "", map[string]any{"email": "portal@example.test", "password": "senha-segura"})
	require.Equal(t, http.StatusOK, login.StatusCode)
	var loginResult struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(login.Body).Decode(&loginResult))
	login.Body.Close()

	me := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/me", loginResult.Token, nil)
	require.Equal(t, http.StatusOK, me.StatusCode)
	var meResult struct {
		Role    string `json:"role"`
		Patient struct {
			ID                 uuid.UUID `json:"id"`
			RelationshipStatus string    `json:"relationshipStatus"`
			Consented          bool      `json:"consented"`
		} `json:"patient"`
	}
	require.NoError(t, json.NewDecoder(me.Body).Decode(&meResult))
	me.Body.Close()
	require.Equal(t, "patient", meResult.Role)
	require.Equal(t, patientID, meResult.Patient.ID)
	require.Equal(t, "active", meResult.Patient.RelationshipStatus)
	require.True(t, meResult.Patient.Consented)

	portal := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/patient/context", loginResult.Token, nil)
	require.Equal(t, http.StatusOK, portal.StatusCode)
	portal.Body.Close()
	denied := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/sessions?patientId="+uuid.NewString(), loginResult.Token, nil)
	require.Equal(t, http.StatusForbidden, denied.StatusCode)
	denied.Body.Close()

	_, err = pool.Exec(ctx, `UPDATE patient_relationship SET status = 'ended', ended_at = now() WHERE patient_id = $1`, patientID)
	require.NoError(t, err)
	ended := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/patient/context", loginResult.Token, nil)
	require.Equal(t, http.StatusNotFound, ended.StatusCode)
	ended.Body.Close()
}
