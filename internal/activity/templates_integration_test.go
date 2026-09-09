//go:build integration

// Biblioteca de templates contra Postgres real (testcontainers):
//
//	go test -tags=integration ./internal/activity/...
//
// Cobre criar, ler, editar no lugar, nova versão após atribuição, arquivar,
// templates globais somente leitura e isolamento entre organizações.
package activity_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/joycesilva/acolhe-api/internal/activity"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("acolhe_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
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
	// Mesmo conteúdo da migration 20260909210000_seed_activity_types.
	seed, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "db", "migrations", "20260909210000_seed_activity_types.sql"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)
	return pool
}

type psychologist struct {
	ctx   context.Context
	orgID uuid.UUID
	psyID uuid.UUID
}

func seedPsychologist(t *testing.T, pool *pgxpool.Pool, slug, email, crp string) psychologist {
	t.Helper()
	ctx := context.Background()
	var orgID, userID, psyID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name, slug) VALUES ($1, $1) RETURNING id`, slug).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (organization_id, email, password_hash, role)
		 VALUES ($1, $2, 'x', 'psychologist') RETURNING id`, orgID, email).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
		 VALUES ($1, 'Dra.', $2, 'SP') RETURNING id`, userID, crp).Scan(&psyID))
	identity := tenant.Identity{OrgID: orgID, UserID: userID, Role: "psychologist"}
	return psychologist{ctx: tenant.WithIdentity(ctx, identity), orgID: orgID, psyID: psyID}
}

// seedActivePatient cria paciente com vínculo ativo para permitir atribuição.
func seedActivePatient(t *testing.T, pool *pgxpool.Pool, p psychologist) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var patientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO patient_profile (organization_id, full_name, status)
		 VALUES ($1, 'Paciente', 'active') RETURNING id`, p.orgID).Scan(&patientID))
	_, err := pool.Exec(ctx,
		`INSERT INTO patient_relationship (patient_id, psychologist_id, status, started_at, requires_health_consent)
		 VALUES ($1, $2, 'active', now(), false)`, patientID, p.psyID)
	require.NoError(t, err)
	return patientID
}

func rpdRequest() activity.TemplateRequest {
	one, ten := 1, 10
	return activity.TemplateRequest{
		Title: "Registro de pensamentos", TypeCode: "record",
		Fields: []activity.FieldInput{
			{Label: "Descreva a situação", FieldType: activity.FieldLongText},
			{Label: "Intensidade da emoção", FieldType: activity.FieldScale, Min: &one, Max: &ten},
			{Label: "Qual distorção você reconhece?", FieldType: activity.FieldMultipleChoice, Options: []string{"Catastrofização", "Leitura mental"}},
		},
	}
}

func TestTemplateLibraryLifecycle(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))
	psi := seedPsychologist(t, pool, "org-1", "psi@org1.dev", "111")

	created, err := svc.CreateTemplate(psi.ctx, rpdRequest())
	require.NoError(t, err)
	assert.Equal(t, int32(1), created.Version)
	assert.True(t, created.OwnedByMe)
	assert.True(t, created.Editable)
	assert.False(t, created.IsGlobal)
	require.Len(t, created.Fields, 3)
	assert.Equal(t, "descreva_a_situacao", created.Fields[0].Code)
	assert.Equal(t, int32(1), created.Fields[0].DisplayOrder)
	assert.JSONEq(t, `{"required":true,"min":1,"max":10}`, string(created.Fields[1].Config))

	list, err := svc.ListTemplates(psi.ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int32(3), list[0].FieldCount)
	assert.True(t, list[0].OwnedByMe)

	// Sem atribuição: edição no lugar, mesma linha, mesma versão, campos trocados.
	edit := rpdRequest()
	edit.Title = "RPD"
	edit.Fields = edit.Fields[:2]
	edited, err := svc.UpdateTemplate(psi.ctx, created.ID, edit)
	require.NoError(t, err)
	assert.Equal(t, created.ID, edited.ID)
	assert.Equal(t, int32(1), edited.Version)
	assert.Equal(t, "RPD", edited.Title)
	require.Len(t, edited.Fields, 2)

	// Atribui uma vez; a próxima edição precisa virar versão 2.
	patientID := seedActivePatient(t, pool, psi)
	_, err = svc.Assign(psi.ctx, activity.AssignRequest{TemplateID: created.ID, PatientID: patientID})
	require.NoError(t, err)

	edit.Title = "RPD v2"
	v2, err := svc.UpdateTemplate(psi.ctx, created.ID, edit)
	require.NoError(t, err)
	assert.NotEqual(t, created.ID, v2.ID)
	assert.Equal(t, int32(2), v2.Version)
	require.NotNil(t, v2.ParentTemplateID)
	assert.Equal(t, created.ID, *v2.ParentTemplateID)
	assert.Equal(t, int64(0), v2.AssignmentCount)

	old, err := svc.GetTemplate(psi.ctx, created.ID)
	require.NoError(t, err)
	assert.True(t, old.Superseded)
	assert.False(t, old.Editable)
	assert.Equal(t, int64(1), old.AssignmentCount, "a atividade continua pinada na v1")
	assert.Len(t, old.Fields, 2, "campos da v1 ficam intactos")

	_, err = svc.UpdateTemplate(psi.ctx, created.ID, edit)
	assert.ErrorIs(t, err, activity.ErrTemplateSuperseded)

	list, err = svc.ListTemplates(psi.ctx)
	require.NoError(t, err)
	require.Len(t, list, 1, "só a versão mais recente aparece na biblioteca")
	assert.Equal(t, v2.ID, list[0].ID)

	// Atribuir pela versão antiga deixa de ser possível; pela nova, funciona.
	_, err = svc.Assign(psi.ctx, activity.AssignRequest{TemplateID: created.ID, PatientID: patientID})
	assert.ErrorIs(t, err, activity.ErrTemplateNotFound)
	_, err = svc.Assign(psi.ctx, activity.AssignRequest{TemplateID: v2.ID, PatientID: patientID})
	require.NoError(t, err)

	// Arquivar tira da biblioteca, mantém a atividade e bloqueia edição.
	archived, err := svc.ArchiveTemplate(psi.ctx, v2.ID)
	require.NoError(t, err)
	assert.True(t, archived.IsArchived)
	assert.False(t, archived.Editable)
	list, err = svc.ListTemplates(psi.ctx)
	require.NoError(t, err)
	assert.Empty(t, list)
	_, err = svc.UpdateTemplate(psi.ctx, v2.ID, edit)
	assert.ErrorIs(t, err, activity.ErrTemplateArchived)
	_, err = svc.ArchiveTemplate(psi.ctx, v2.ID)
	assert.ErrorIs(t, err, activity.ErrTemplateArchived)
	assignments, err := svc.ListAssignments(psi.ctx, patientID)
	require.NoError(t, err)
	assert.Len(t, assignments, 2, "atividades sobrevivem ao arquivamento")

	// Tipo base desconhecido é erro de validação, não 500.
	bad := rpdRequest()
	bad.TypeCode = "inexistente"
	_, err = svc.CreateTemplate(psi.ctx, bad)
	assert.ErrorIs(t, err, activity.ErrInvalidTemplate)
}

func TestTemplateLibraryIsolationAndGlobals(t *testing.T) {
	pool := setupDB(t)
	svc := activity.NewService(db.New(pool))
	org1 := seedPsychologist(t, pool, "org-1", "psi@org1.dev", "111")
	org2 := seedPsychologist(t, pool, "org-2", "psi@org2.dev", "222")

	mine, err := svc.CreateTemplate(org1.ctx, rpdRequest())
	require.NoError(t, err)

	// Template global (organization_id nulo), autoria de uma psicóloga qualquer.
	var typeID, globalID uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT id FROM activity_type WHERE code = 'scale'`).Scan(&typeID))
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO activity_template (type_id, organization_id, author_id, title)
		 VALUES ($1, NULL, $2, 'Check-in de humor (Acolhe)') RETURNING id`, typeID, org2.psyID).Scan(&globalID))

	// org2 não vê nem edita o template da org1.
	_, err = svc.GetTemplate(org2.ctx, mine.ID)
	assert.ErrorIs(t, err, activity.ErrTemplateNotFound)
	_, err = svc.UpdateTemplate(org2.ctx, mine.ID, rpdRequest())
	assert.ErrorIs(t, err, activity.ErrTemplateNotFound)
	_, err = svc.ArchiveTemplate(org2.ctx, mine.ID)
	assert.ErrorIs(t, err, activity.ErrTemplateNotFound)

	list2, err := svc.ListTemplates(org2.ctx)
	require.NoError(t, err)
	require.Len(t, list2, 1, "org2 vê só o global")
	assert.Equal(t, globalID, list2[0].ID)
	assert.True(t, list2[0].IsGlobal)
	assert.False(t, list2[0].OwnedByMe, "global nunca conta como meu, mesmo sendo a autora")

	list1, err := svc.ListTemplates(org1.ctx)
	require.NoError(t, err)
	assert.Len(t, list1, 2, "org1 vê o próprio e o global")

	// Global é somente leitura para todo mundo.
	global, err := svc.GetTemplate(org1.ctx, globalID)
	require.NoError(t, err)
	assert.True(t, global.IsGlobal)
	assert.False(t, global.Editable)
	_, err = svc.UpdateTemplate(org1.ctx, globalID, rpdRequest())
	assert.ErrorIs(t, err, activity.ErrTemplateReadOnly)
	_, err = svc.ArchiveTemplate(org2.ctx, globalID)
	assert.ErrorIs(t, err, activity.ErrTemplateReadOnly)

	// Paciente não cria template.
	patientCtx := tenant.WithIdentity(context.Background(), tenant.Identity{
		OrgID: org1.orgID, UserID: uuid.New(), Role: "patient",
	})
	_, err = svc.CreateTemplate(patientCtx, rpdRequest())
	assert.ErrorIs(t, err, activity.ErrPsychologistRequired)
}
