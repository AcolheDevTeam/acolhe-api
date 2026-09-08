package checkin_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/checkin"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

func TestCreateCheckinValidatesMoodBeforeDatabase(t *testing.T) {
	service := checkin.NewService(&testsupport.FakeQuerier{})
	for _, mood := range []int32{-1, 0, 6, 99} {
		_, err := service.Create(context.Background(), checkin.CreateRequest{
			PatientID: uuid.New(),
			Mood:      mood,
		})
		assert.ErrorIs(t, err, checkin.ErrInvalidMood)
	}
}

func TestCreateCheckinEnforcesOrganization(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	patientID := uuid.New()
	fake := &testsupport.FakeQuerier{
		PatientInOrgFn: func(_ context.Context, arg db.PatientInOrgParams) (bool, error) {
			assert.Equal(t, patientID, arg.PatientID)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return false, nil
		},
	}
	_, err := checkin.NewService(fake).Create(
		tenant.WithIdentity(context.Background(), identity),
		checkin.CreateRequest{PatientID: patientID, Mood: 3},
	)
	assert.ErrorIs(t, err, checkin.ErrPatientNotInOrg)
}

func TestCreateAndListCheckinKeepTenantPredicate(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	patientID, checkinID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	note := "Hoje me senti melhor."
	fake := &testsupport.FakeQuerier{
		PatientInOrgFn: func(_ context.Context, arg db.PatientInOrgParams) (bool, error) {
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return true, nil
		},
		CreateCheckinFn: func(_ context.Context, arg db.CreateCheckinParams) (db.Checkin, error) {
			assert.Equal(t, patientID, arg.PatientID)
			assert.Equal(t, int32(4), arg.Mood)
			return db.Checkin{
				ID: checkinID, PatientID: arg.PatientID, Mood: arg.Mood,
				Note: arg.Note, CreatedAt: now,
			}, nil
		},
		ListCheckinsFn: func(_ context.Context, arg db.ListCheckinsByPatientParams) ([]db.Checkin, error) {
			assert.Equal(t, patientID, arg.PatientID)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return []db.Checkin{{
				ID: checkinID, PatientID: patientID, Mood: 4,
				Note: &note, CreatedAt: now,
			}}, nil
		},
	}
	service := checkin.NewService(fake)
	ctx := tenant.WithIdentity(context.Background(), identity)
	created, err := service.Create(ctx, checkin.CreateRequest{
		PatientID: patientID, Mood: 4, Note: &note,
	})
	require.NoError(t, err)
	assert.Equal(t, checkinID, created.ID)
	items, err := service.List(ctx, patientID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, checkinID, items[0].ID)
}
