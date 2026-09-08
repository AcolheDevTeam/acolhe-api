package account_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/account"
	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

func TestLoginReturnsTenantBoundToken(t *testing.T) {
	passwordHash, err := auth.HashPassword("safe-password")
	require.NoError(t, err)
	userID, organizationID := uuid.New(), uuid.New()
	fake := &testsupport.FakeQuerier{
		GetUserByEmailFn: func(_ context.Context, email string) (db.GetUserByEmailRow, error) {
			assert.Equal(t, "psi@example.test", email)
			return db.GetUserByEmailRow{
				ID: userID, OrganizationID: &organizationID,
				Email: email, PasswordHash: passwordHash, Role: "psychologist",
			}, nil
		},
	}
	result, err := account.NewService(fake, "secret").Login(
		context.Background(),
		"psi@example.test",
		"safe-password",
	)
	require.NoError(t, err)
	assert.Equal(t, userID, result.User.ID)
	assert.Equal(t, &organizationID, result.User.OrganizationID)
	claims, err := auth.ParseToken("secret", result.Token)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.UserID)
	assert.Equal(t, organizationID.String(), claims.OrganizationID)
}

func TestLoginDoesNotRevealWhetherEmailOrPasswordFailed(t *testing.T) {
	passwordHash, err := auth.HashPassword("safe-password")
	require.NoError(t, err)
	tests := []struct {
		name string
		fake *testsupport.FakeQuerier
	}{
		{
			name: "unknown email",
			fake: &testsupport.FakeQuerier{
				GetUserByEmailFn: func(context.Context, string) (db.GetUserByEmailRow, error) {
					return db.GetUserByEmailRow{}, pgx.ErrNoRows
				},
			},
		},
		{
			name: "wrong password",
			fake: &testsupport.FakeQuerier{
				GetUserByEmailFn: func(_ context.Context, email string) (db.GetUserByEmailRow, error) {
					organizationID := uuid.New()
					return db.GetUserByEmailRow{
						ID: uuid.New(), OrganizationID: &organizationID,
						Email: email, PasswordHash: passwordHash, Role: "patient",
					}, nil
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := account.NewService(test.fake, "secret").Login(
				context.Background(),
				"user@example.test",
				"wrong",
			)
			assert.ErrorIs(t, err, account.ErrInvalidCredentials)
		})
	}
}

func TestMeUsesOnlyAuthenticatedUserID(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "patient",
	}
	fake := &testsupport.FakeQuerier{
		GetUserByIDFn: func(_ context.Context, id uuid.UUID) (db.GetUserByIDRow, error) {
			assert.Equal(t, identity.UserID, id)
			return db.GetUserByIDRow{
				ID: id, OrganizationID: &identity.OrgID,
				Email: "patient@example.test", Role: "patient",
			}, nil
		},
	}
	user, err := account.NewService(fake, "secret").Me(
		tenant.WithIdentity(context.Background(), identity),
	)
	require.NoError(t, err)
	assert.Equal(t, identity.UserID, user.ID)

	_, err = account.NewService(fake, "secret").Me(context.Background())
	assert.ErrorIs(t, err, account.ErrUserNotFound)
}
