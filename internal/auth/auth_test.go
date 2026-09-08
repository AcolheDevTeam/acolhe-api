package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/auth"
)

func TestTokenRoundTripCarriesTenantIdentity(t *testing.T) {
	userID, organizationID := uuid.NewString(), uuid.NewString()
	token, err := auth.GenerateToken(
		"test-secret",
		userID,
		"psychologist",
		organizationID,
	)
	require.NoError(t, err)
	claims, err := auth.ParseToken("test-secret", token)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.UserID)
	assert.Equal(t, userID, claims.Subject)
	assert.Equal(t, organizationID, claims.OrganizationID)
	assert.Equal(t, "psychologist", claims.Role)
	assert.WithinDuration(t, time.Now().Add(7*24*time.Hour), claims.ExpiresAt.Time, 5*time.Second)
}

func TestParseTokenRejectsSecurityBoundaryFailures(t *testing.T) {
	userID, organizationID := uuid.NewString(), uuid.NewString()
	valid, err := auth.GenerateToken("correct", userID, "patient", organizationID)
	require.NoError(t, err)
	expired := signedClaims(t, jwt.SigningMethodHS256, "correct", auth.Claims{
		UserID: userID, Role: "patient", OrganizationID: organizationID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID, ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	})
	wrongAlgorithm := signedClaims(t, jwt.SigningMethodHS384, "correct", auth.Claims{
		UserID: userID, Role: "patient", OrganizationID: organizationID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	mismatchedSubject := signedClaims(t, jwt.SigningMethodHS256, "correct", auth.Claims{
		UserID: userID, Role: "patient", OrganizationID: organizationID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})

	tests := []struct {
		name, secret, token string
	}{
		{name: "wrong secret", secret: "wrong", token: valid},
		{name: "expired", secret: "correct", token: expired},
		{name: "wrong algorithm", secret: "correct", token: wrongAlgorithm},
		{name: "mismatched subject", secret: "correct", token: mismatchedSubject},
		{name: "malformed", secret: "correct", token: "not-a-jwt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := auth.ParseToken(test.secret, test.token)
			assert.Error(t, err)
		})
	}
}

func TestGenerateTokenRejectsInvalidIdentity(t *testing.T) {
	tests := []struct {
		name, secret, userID, role, organizationID string
	}{
		{name: "empty secret", userID: uuid.NewString(), role: "patient", organizationID: uuid.NewString()},
		{name: "invalid user", secret: "secret", userID: "invalid", role: "patient", organizationID: uuid.NewString()},
		{name: "invalid role", secret: "secret", userID: uuid.NewString(), role: "owner", organizationID: uuid.NewString()},
		{name: "missing organization", secret: "secret", userID: uuid.NewString(), role: "patient"},
		{name: "platform admin with organization", secret: "secret", userID: uuid.NewString(), role: "platform_admin", organizationID: uuid.NewString()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := auth.GenerateToken(
				test.secret,
				test.userID,
				test.role,
				test.organizationID,
			)
			assert.Error(t, err)
		})
	}
}

func TestPasswordHashDoesNotAcceptWrongCredential(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery staple")
	require.NoError(t, err)
	assert.True(t, auth.CheckPassword(hash, "correct horse battery staple"))
	assert.False(t, auth.CheckPassword(hash, "wrong"))
	assert.NotContains(t, hash, "correct horse battery staple")
}

func signedClaims(
	t *testing.T,
	method jwt.SigningMethod,
	secret string,
	claims auth.Claims,
) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	require.NoError(t, err)
	return token
}
