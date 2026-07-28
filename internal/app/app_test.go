package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/app"
	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

const testSecret = "test-secret"

// --- helpers de requisição ---

func authed(t *testing.T, method, target, body string, userID, orgID uuid.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	token, err := auth.GenerateToken(testSecret, userID.String(), "psychologist", orgID.String())
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func serve(srv *httptest.Server, req *http.Request) *http.Response {
	req.RequestURI = ""
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
	resp, err := srv.Client().Do(req)
	if err != nil {
		panic(err)
	}
	return resp
}

// newServer sobe um httptest.Server servindo a aplicação montada sobre o fake.
func newServer(t *testing.T, q db.Querier) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(app.New(nil, q, nil, testSecret).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// --- testes ---

func TestHealth_OK(t *testing.T) {
	srv := newServer(t, &testsupport.FakeQuerier{})
	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHealth_DBDown(t *testing.T) {
	q := &testsupport.FakeQuerier{
		HealthCheckFn: func(context.Context) (int32, error) { return 0, errors.New("down") },
	}
	srv := newServer(t, q)
	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestLogin_OK(t *testing.T) {
	hash, err := auth.HashPassword("segredo123")
	require.NoError(t, err)
	userID := uuid.New()
	orgID := uuid.New()
	q := &testsupport.FakeQuerier{
		GetUserByEmailFn: func(_ context.Context, email string) (db.GetUserByEmailRow, error) {
			return db.GetUserByEmailRow{
				ID: userID, OrganizationID: &orgID, Email: email,
				PasswordHash: hash, Role: "psychologist", Status: "active",
			}, nil
		},
	}
	srv := newServer(t, q)
	resp, err := http.Post(srv.URL+"/login", "application/json",
		strings.NewReader(`{"email":"psi@acolhe.dev","password":"segredo123"}`))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Token string `json:"token"`
		User  struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.NotEmpty(t, out.Token)
	assert.Equal(t, "psi@acolhe.dev", out.User.Email)
}

func TestLogin_BadCredentials(t *testing.T) {
	hash, _ := auth.HashPassword("a-senha-certa")
	q := &testsupport.FakeQuerier{
		GetUserByEmailFn: func(_ context.Context, email string) (db.GetUserByEmailRow, error) {
			return db.GetUserByEmailRow{ID: uuid.New(), Email: email, PasswordHash: hash, Role: "psychologist"}, nil
		},
	}
	srv := newServer(t, q)
	resp, err := http.Post(srv.URL+"/login", "application/json",
		strings.NewReader(`{"email":"psi@acolhe.dev","password":"errada"}`))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMe_Unauthorized(t *testing.T) {
	srv := newServer(t, &testsupport.FakeQuerier{})
	resp, err := http.Get(srv.URL + "/me")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestPatients_List(t *testing.T) {
	orgID := uuid.New()
	userID := uuid.New()
	psyID := uuid.New()
	q := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(_ context.Context, gotUser uuid.UUID) (db.GetPsychologistByUserRow, error) {
			assert.Equal(t, userID, gotUser)
			return db.GetPsychologistByUserRow{ID: psyID}, nil
		},
		ListPatientsByPsychFn: func(_ context.Context, arg db.ListPatientsByPsychologistParams) ([]db.ListPatientsByPsychologistRow, error) {
			assert.Equal(t, orgID, arg.OrganizationID)
			assert.Equal(t, psyID, arg.PsychologistID)
			return []db.ListPatientsByPsychologistRow{
				{ID: uuid.New(), FullName: "Ana", Status: "active", CreatedAt: time.Now()},
				{ID: uuid.New(), FullName: "Bruno", Status: "active", CreatedAt: time.Now()},
			}, nil
		},
	}
	srv := newServer(t, q)
	resp := serve(srv, authed(t, http.MethodGet, "/patients", "", userID, orgID))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Len(t, out, 2)
}

func TestCreateSession_NoActiveRelationship_403(t *testing.T) {
	psyID := uuid.New()
	q := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psyID}, nil
		},
		GetActiveRelationshipFn: func(context.Context, db.GetActiveRelationshipParams) (db.GetActiveRelationshipRow, error) {
			return db.GetActiveRelationshipRow{}, errors.New("no rows") // sem vínculo
		},
	}
	srv := newServer(t, q)
	body := `{"patientId":"` + uuid.New().String() + `","occurredAt":"2026-05-29T10:00:00Z","notes":"Evolução"}`
	resp := serve(srv, authed(t, http.MethodPost, "/sessions", body, uuid.New(), uuid.New()))
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestCreateSession_OK_201(t *testing.T) {
	psyID := uuid.New()
	patientID := uuid.New()
	q := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psyID}, nil
		},
		GetActiveRelationshipFn: func(context.Context, db.GetActiveRelationshipParams) (db.GetActiveRelationshipRow, error) {
			return db.GetActiveRelationshipRow{ID: uuid.New(), Status: "active"}, nil
		},
		CreateSessionFn: func(_ context.Context, arg db.CreateSessionParams) (db.CreateSessionRow, error) {
			return db.CreateSessionRow{
				ID: uuid.New(), PatientID: arg.PatientID, PsychologistID: arg.PsychologistID,
				OccurredAt: arg.OccurredAt, Status: arg.Status, CreatedAt: time.Now(),
			}, nil
		},
	}
	srv := newServer(t, q)
	body := `{"patientId":"` + patientID.String() + `","occurredAt":"2026-05-29T10:00:00Z","notes":"Evolução"}`
	resp := serve(srv, authed(t, http.MethodPost, "/sessions", body, uuid.New(), uuid.New()))
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestCreateAppointment_Conflict_409(t *testing.T) {
	q := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: uuid.New()}, nil
		},
		CountAppointmentConflFn: func(context.Context, db.CountAppointmentConflictsParams) (int64, error) {
			return 1, nil // já há sobreposição
		},
	}
	srv := newServer(t, q)
	body := `{"patientId":"` + uuid.New().String() + `","scheduledFor":"2026-05-29T10:00:00Z","durationMinutes":50}`
	resp := serve(srv, authed(t, http.MethodPost, "/appointments", body, uuid.New(), uuid.New()))
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestCreateAppointment_OK_201(t *testing.T) {
	q := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: uuid.New()}, nil
		},
		CountAppointmentConflFn: func(context.Context, db.CountAppointmentConflictsParams) (int64, error) {
			return 0, nil
		},
		CreateAppointmentFn: func(_ context.Context, arg db.CreateAppointmentParams) (db.CreateAppointmentRow, error) {
			return db.CreateAppointmentRow{
				ID: uuid.New(), PatientID: arg.PatientID, PsychologistID: arg.PsychologistID,
				ScheduledFor: arg.ScheduledFor, DurationMinutes: arg.DurationMinutes,
				Modality: arg.Modality, Status: "scheduled", CreatedAt: time.Now(),
			}, nil
		},
	}
	srv := newServer(t, q)
	body := `{"patientId":"` + uuid.New().String() + `","scheduledFor":"2026-05-29T10:00:00Z","durationMinutes":50}`
	resp := serve(srv, authed(t, http.MethodPost, "/appointments", body, uuid.New(), uuid.New()))
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}
