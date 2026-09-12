//go:build integration

// Submissão tipada da paciente contra Postgres real (testcontainers):
//
//	go test -tags=integration ./internal/activity/...
//
// Cobre o formulário que a paciente recebe, a gravação de um valor por campo na
// coluna tipada certa, atomicidade com o status, replay idempotente, versão
// pinada, integridade recusada pelo banco e isolamento entre organizações.
package activity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/activity"
	"github.com/joycesilva/acolhe-api/internal/app"
	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// seedPatientUser liga um patient_profile a um usuário e devolve o contexto da
// paciente autenticada, que é quem responde.
func seedPatientUser(t *testing.T, pool *pgxpool.Pool, orgID, patientID uuid.UUID, email string) context.Context {
	ctx, _ := seedPatientUserWithID(t, pool, orgID, patientID, email)
	return ctx
}

func seedPatientUserWithID(t *testing.T, pool *pgxpool.Pool, orgID, patientID uuid.UUID, email string) (context.Context, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var userID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (organization_id, email, password_hash, role)
		 VALUES ($1, $2, 'x', 'patient') RETURNING id`, orgID, email).Scan(&userID))
	_, err := pool.Exec(ctx,
		`UPDATE patient_profile SET user_id = $1 WHERE id = $2`, userID, patientID)
	require.NoError(t, err)
	return tenant.WithIdentity(ctx, tenant.Identity{OrgID: orgID, UserID: userID, Role: "patient"}), userID
}

// assignRPD cria o template de exemplo, atribui à paciente e devolve os ids.
func assignRPD(t *testing.T, svc *activity.Service, psi psychologist, patientID uuid.UUID) (uuid.UUID, *activity.TemplateDetail) {
	t.Helper()
	template, err := svc.CreateTemplate(psi.ctx, rpdRequest())
	require.NoError(t, err)
	due := time.Now().Add(48 * time.Hour)
	assigned, err := svc.Assign(psi.ctx, activity.AssignRequest{
		TemplateID: template.ID, PatientID: patientID, DueAt: &due,
	})
	require.NoError(t, err)
	return assigned.ID, template
}

// respostaValida devolve um valor para cada campo do rpdRequest, na ordem.
func respostaValida(submissionID uuid.UUID, template *activity.TemplateDetail) activity.SubmissionRequest {
	relato := "Briguei com minha irmã e fiquei remoendo a noite toda."
	intensidade := float64(8)
	return activity.SubmissionRequest{
		SubmissionID:    submissionID,
		TemplateVersion: template.Version,
		Values: []activity.SubmissionValue{
			{FieldCode: template.Fields[0].Code, Kind: activity.FieldLongText, Text: &relato},
			{FieldCode: template.Fields[1].Code, Kind: activity.FieldScale, Number: &intensidade},
			{FieldCode: template.Fields[2].Code, Kind: activity.FieldMultipleChoice, Choices: []string{"Catastrofização"}},
		},
	}
}

func TestPatientActivity_FormularioDaVersaoPinada(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))
	psi := seedPsychologist(t, pool, "org-form", "psi@form.dev", "201")
	patientID := seedActivePatient(t, pool, psi)
	patientCtx := seedPatientUser(t, pool, psi.orgID, patientID, "paciente@form.dev")

	assignmentID, template := assignRPD(t, svc, psi, patientID)

	detail, err := svc.PatientActivity(patientCtx, assignmentID)
	require.NoError(t, err)
	assert.Equal(t, "Registro de pensamentos", detail.Title)
	assert.Equal(t, "pending", detail.Status)
	assert.True(t, detail.CanRespond)
	assert.Equal(t, template.Version, detail.TemplateVersion)
	require.Len(t, detail.Fields, 3)
	// A ordem é a do builder e é a ordem em que a paciente responde.
	assert.Equal(t, int32(1), detail.Fields[0].DisplayOrder)
	assert.Equal(t, activity.FieldLongText, detail.Fields[0].FieldType)
	assert.JSONEq(t, `{"required":true,"min":1,"max":10}`, string(detail.Fields[1].Config))
	assert.Equal(t, activity.FieldMultipleChoice, detail.Fields[2].FieldType)
}

func TestSubmitTyped_GravaUmValorPorCampoEFechaOCiclo(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))
	psi := seedPsychologist(t, pool, "org-sub", "psi@sub.dev", "202")
	patientID := seedActivePatient(t, pool, psi)
	patientCtx := seedPatientUser(t, pool, psi.orgID, patientID, "paciente@sub.dev")
	assignmentID, template := assignRPD(t, svc, psi, patientID)

	submissionID := uuid.New()
	response, err := svc.SubmitTyped(patientCtx, assignmentID, respostaValida(submissionID, template))
	require.NoError(t, err)
	require.NotNil(t, response.SubmittedAt)
	assert.False(t, response.IsDraft)

	ctx := context.Background()

	// Status e resposta mudam juntos.
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM activity_assignment WHERE id = $1`, assignmentID).Scan(&status))
	assert.Equal(t, "submitted", status)

	// Um valor por campo, cada um na coluna tipada certa.
	rows, err := pool.Query(ctx, `
		SELECT v.field_code, v.value_text, v.value_number, v.value_boolean, v.value_datetime, v.value_json,
		       num_nonnulls(v.value_text, v.value_number, v.value_boolean, v.value_datetime, v.value_json, v.attachment_id)
		FROM activity_response_value v
		JOIN activity_field f ON f.id = v.field_id
		WHERE v.response_id = $1
		ORDER BY f.display_order`, response.ID)
	require.NoError(t, err)
	defer rows.Close()

	type stored struct {
		code    string
		text    *string
		number  *float64
		boolean *bool
		moment  *time.Time
		raw     []byte
		filled  int
	}
	var values []stored
	for rows.Next() {
		var s stored
		require.NoError(t, rows.Scan(&s.code, &s.text, &s.number, &s.boolean, &s.moment, &s.raw, &s.filled))
		values = append(values, s)
	}
	require.NoError(t, rows.Err())
	require.Len(t, values, 3)
	for _, value := range values {
		assert.Equal(t, 1, value.filled, "campo %q precisa ter exatamente uma coluna preenchida", value.code)
	}
	require.NotNil(t, values[0].text)
	assert.Contains(t, *values[0].text, "minha irmã")
	require.NotNil(t, values[1].number)
	assert.Equal(t, float64(8), *values[1].number)
	assert.JSONEq(t, `["Catastrofização"]`, string(values[2].raw))

	// O banco considera a submissão completa, então a revisão é liberada.
	var complete bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT activity_submission_is_complete($1)`, assignmentID).Scan(&complete))
	assert.True(t, complete, "submissão precisa ser considerada completa para a revisão")

	// E a psicóloga enxerga a resposta ordenada e tipada na revisão.
	review, err := svc.Get(psi.ctx, assignmentID)
	require.NoError(t, err)
	assert.Equal(t, "submitted", review.State)
	require.NotNil(t, review.Submission)
	require.Len(t, review.Submission.Fields, 3)
	assert.Equal(t, "text", review.Submission.Fields[0].Kind)
	assert.Equal(t, "number", review.Submission.Fields[1].Kind)
	assert.Equal(t, "json", review.Submission.Fields[2].Kind)

	// E consegue concluir a revisão.
	reviewed, err := svc.MarkReviewed(psi.ctx, assignmentID)
	require.NoError(t, err)
	assert.Equal(t, "reviewed", reviewed.State)
}

func TestSubmitTyped_ReplayDaMesmaSubmissaoEIdempotente(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))
	psi := seedPsychologist(t, pool, "org-replay", "psi@replay.dev", "203")
	patientID := seedActivePatient(t, pool, psi)
	patientCtx := seedPatientUser(t, pool, psi.orgID, patientID, "paciente@replay.dev")
	assignmentID, template := assignRPD(t, svc, psi, patientID)

	submissionID := uuid.New()
	first, err := svc.SubmitTyped(patientCtx, assignmentID, respostaValida(submissionID, template))
	require.NoError(t, err)

	// Mesmo submissionId (rede caiu, usuária tocou duas vezes): devolve o original.
	again, err := svc.SubmitTyped(patientCtx, assignmentID, respostaValida(submissionID, template))
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID)

	var responses, values int
	ctx := context.Background()
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM activity_response WHERE assignment_id = $1`, assignmentID).Scan(&responses))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM activity_response_value WHERE response_id = $1`, first.ID).Scan(&values))
	assert.Equal(t, 1, responses, "replay não pode criar uma segunda resposta")
	assert.Equal(t, 3, values, "replay não pode duplicar valores")

	// submissionId diferente numa atividade já respondida é conflito, não replay.
	_, err = svc.SubmitTyped(patientCtx, assignmentID, respostaValida(uuid.New(), template))
	require.ErrorIs(t, err, activity.ErrAlreadySubmitted)
}

func TestSubmitTyped_RecusaVersaoEConteudoInvalido(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))
	psi := seedPsychologist(t, pool, "org-inval", "psi@inval.dev", "204")
	patientID := seedActivePatient(t, pool, psi)
	patientCtx := seedPatientUser(t, pool, psi.orgID, patientID, "paciente@inval.dev")
	assignmentID, template := assignRPD(t, svc, psi, patientID)

	t.Run("versão diferente da pinada", func(t *testing.T) {
		req := respostaValida(uuid.New(), template)
		req.TemplateVersion = template.Version + 1
		_, err := svc.SubmitTyped(patientCtx, assignmentID, req)
		require.ErrorIs(t, err, activity.ErrSubmissionVersionMismatch)
	})

	t.Run("opção que não existe no campo", func(t *testing.T) {
		req := respostaValida(uuid.New(), template)
		req.Values[2].Choices = []string{"Bola de cristal"}
		_, err := svc.SubmitTyped(patientCtx, assignmentID, req)
		require.ErrorIs(t, err, activity.ErrInvalidSubmission)
	})

	t.Run("escala fora da faixa", func(t *testing.T) {
		req := respostaValida(uuid.New(), template)
		fora := float64(99)
		req.Values[1].Number = &fora
		_, err := svc.SubmitTyped(patientCtx, assignmentID, req)
		require.ErrorIs(t, err, activity.ErrInvalidSubmission)
	})

	t.Run("sem submissionId", func(t *testing.T) {
		req := respostaValida(uuid.Nil, template)
		_, err := svc.SubmitTyped(patientCtx, assignmentID, req)
		require.ErrorIs(t, err, activity.ErrInvalidSubmission)
	})

	// Nenhuma das recusas pode ter deixado rastro.
	ctx := context.Background()
	var responses int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM activity_response WHERE assignment_id = $1`, assignmentID).Scan(&responses))
	assert.Zero(t, responses, "submissão recusada não pode criar resposta")

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM activity_assignment WHERE id = $1`, assignmentID).Scan(&status))
	assert.Equal(t, "pending", status, "submissão recusada não pode mudar o status")
}

func TestSubmitTyped_IsolamentoEntreOrganizacoes(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))

	psiA := seedPsychologist(t, pool, "org-a", "psi@a.dev", "205")
	patientA := seedActivePatient(t, pool, psiA)
	assignmentA, templateA := assignRPD(t, svc, psiA, patientA)

	psiB := seedPsychologist(t, pool, "org-b", "psi@b.dev", "206")
	patientB := seedActivePatient(t, pool, psiB)
	patientBCtx := seedPatientUser(t, pool, psiB.orgID, patientB, "paciente@b.dev")

	// A paciente da org B não enxerga nem responde a atividade da org A.
	_, err := svc.PatientActivity(patientBCtx, assignmentA)
	require.ErrorIs(t, err, activity.ErrAssignmentNotFound)

	_, err = svc.SubmitTyped(patientBCtx, assignmentA, respostaValida(uuid.New(), templateA))
	require.ErrorIs(t, err, activity.ErrAssignmentNotFound)

	var responses int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity_response WHERE assignment_id = $1`, assignmentA).Scan(&responses))
	assert.Zero(t, responses)
}

func TestSubmitTyped_SomentePaciente(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))
	psi := seedPsychologist(t, pool, "org-role", "psi@role.dev", "207")
	patientID := seedActivePatient(t, pool, psi)
	assignmentID, template := assignRPD(t, svc, psi, patientID)

	// A própria psicóloga não pode responder pela paciente.
	_, err := svc.SubmitTyped(psi.ctx, assignmentID, respostaValida(uuid.New(), template))
	require.ErrorIs(t, err, activity.ErrPatientRequired)

	_, err = svc.PatientActivity(psi.ctx, assignmentID)
	require.ErrorIs(t, err, activity.ErrPatientRequired)
}

// TestSubmissionRouteReachableByPatient roda pelo HTTP real, com o middleware de
// papel no caminho — que é onde o bug estava. O guard de middleware/tenant.go
// bloqueia todo prefixo /activities para pacientes, então a rota de submissão
// precisa morar sob /patient. Teste de serviço não pega isso: ele não passa pelo
// roteamento.
func TestSubmissionRouteReachableByPatient(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))
	psi := seedPsychologist(t, pool, "org-rota", "psi@rota.dev", "208")
	patientID := seedActivePatient(t, pool, psi)
	_, patientUserID := seedPatientUserWithID(t, pool, psi.orgID, patientID, "paciente@rota.dev")
	assignmentID, template := assignRPD(t, svc, psi, patientID)

	srv := httptest.NewServer(app.New(pool, db.New(pool), nil, "secret").Handler())
	t.Cleanup(srv.Close)

	token, err := auth.GenerateToken("secret", patientUserID.String(), "patient", psi.orgID.String())
	require.NoError(t, err)

	post := func(path string, payload any) *http.Response {
		body, marshalErr := json.Marshal(payload)
		require.NoError(t, marshalErr)
		req, reqErr := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(body))
		require.NoError(t, reqErr)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, doErr := srv.Client().Do(req)
		require.NoError(t, doErr)
		return resp
	}

	// O caminho antigo continua barrado para a paciente pelo guard de papel.
	antiga := post("/activities/assignments/"+assignmentID.String()+"/responses",
		respostaValida(uuid.New(), template))
	defer antiga.Body.Close()
	assert.Equal(t, http.StatusForbidden, antiga.StatusCode,
		"prefixo /activities segue bloqueado para pacientes")

	// O formulário e a submissão, sob /patient, funcionam.
	form, err := srv.Client().Do(func() *http.Request {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/patient/activities/"+assignmentID.String(), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		return req
	}())
	require.NoError(t, err)
	defer form.Body.Close()
	require.Equal(t, http.StatusOK, form.StatusCode)

	enviada := post("/patient/activities/"+assignmentID.String()+"/responses",
		respostaValida(uuid.New(), template))
	defer enviada.Body.Close()
	require.Equal(t, http.StatusCreated, enviada.StatusCode)

	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM activity_assignment WHERE id = $1`, assignmentID).Scan(&status))
	assert.Equal(t, "submitted", status)
}
