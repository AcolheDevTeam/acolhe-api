package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/middleware"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

func TestAuthAndTenantGuards(t *testing.T) {
	const secret = "middleware-secret"
	userID, organizationID := uuid.New(), uuid.New()
	token, err := auth.GenerateToken(
		secret,
		userID.String(),
		"psychologist",
		organizationID.String(),
	)
	require.NoError(t, err)

	tests := []struct {
		name          string
		authorization string
		wantCode      int
	}{
		{name: "missing token", wantCode: http.StatusUnauthorized},
		{name: "invalid token", authorization: "Bearer invalid", wantCode: http.StatusUnauthorized},
		{name: "wrong scheme", authorization: token, wantCode: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context := newEchoContext(http.MethodGet, "/patients", test.authorization)
			err := protectedHandler(secret, func(echo.Context) error {
				return errors.New("must not reach protected handler")
			})(context)
			require.Error(t, err)
			var httpError *echo.HTTPError
			require.ErrorAs(t, err, &httpError)
			assert.Equal(t, test.wantCode, httpError.Code)
		})
	}

	context := newEchoContext(http.MethodGet, "/patients", "Bearer "+token)
	context.Request().Header.Set("X-Organization-ID", uuid.NewString())
	reached := false
	err = protectedHandler(secret, func(c echo.Context) error {
		reached = true
		identity, ok := tenant.FromContext(c.Request().Context())
		require.True(t, ok)
		assert.Equal(t, userID, identity.UserID)
		assert.Equal(t, organizationID, identity.OrgID)
		assert.Equal(t, "psychologist", identity.Role)
		return c.NoContent(http.StatusNoContent)
	})(context)
	require.NoError(t, err)
	assert.True(t, reached)
}

func TestPublicRouteSkipsAuthAndTenant(t *testing.T) {
	context := newEchoContext(http.MethodGet, "/health", "")
	err := protectedHandler("secret", func(c echo.Context) error {
		_, ok := tenant.FromContext(c.Request().Context())
		assert.False(t, ok)
		return c.NoContent(http.StatusOK)
	})(context)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, context.Response().Status)
}

func TestAuditWritesOnlySuccessfulMutations(t *testing.T) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	logged := make(chan db.WriteAuditLogParams, 1)
	fake := &testsupport.FakeQuerier{
		WriteAuditLogFn: func(_ context.Context, entry db.WriteAuditLogParams) error {
			logged <- entry
			return nil
		},
	}
	context := newEchoContext(http.MethodPost, "/patients/:id", "")
	context.SetParamNames("id")
	context.SetParamValues("patient-one")
	context.SetRequest(context.Request().WithContext(
		tenant.WithIdentity(context.Request().Context(), identity),
	))
	err := middleware.Audit(fake)(func(c echo.Context) error {
		return c.NoContent(http.StatusCreated)
	})(context)
	require.NoError(t, err)

	select {
	case entry := <-logged:
		assert.Equal(t, identity.UserID, entry.ActorUserID)
		require.NotNil(t, entry.OrganizationID)
		assert.Equal(t, identity.OrgID, *entry.OrganizationID)
		assert.Equal(t, http.MethodPost, entry.Action)
		assert.Equal(t, "patients", entry.ResourceType)
		assert.Equal(t, "patient-one", entry.ResourceID)
	case <-time.After(time.Second):
		t.Fatal("audit mutation was not written")
	}

	failed := newEchoContext(http.MethodPost, "/patients", "")
	failed.SetRequest(failed.Request().WithContext(
		tenant.WithIdentity(failed.Request().Context(), identity),
	))
	expected := echo.NewHTTPError(http.StatusConflict, "conflict")
	err = middleware.Audit(fake)(func(echo.Context) error { return expected })(failed)
	assert.ErrorIs(t, err, expected)
	select {
	case <-logged:
		t.Fatal("failed mutation must not be audited")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestTenantTxWithoutPoolPreservesInjectedQuerier(t *testing.T) {
	context := newEchoContext(http.MethodGet, "/patients", "")
	expected := &testsupport.FakeQuerier{}
	context.SetRequest(context.Request().WithContext(
		tenant.WithQueries(context.Request().Context(), expected),
	))
	err := middleware.TenantTx(nil)(func(c echo.Context) error {
		assert.Same(t, expected, tenant.Queries(c.Request().Context(), nil))
		return nil
	})(context)
	require.NoError(t, err)
}

func protectedHandler(secret string, next echo.HandlerFunc) echo.HandlerFunc {
	return middleware.Auth(secret)(middleware.Tenant()(next))
}

func newEchoContext(method, path, authorization string) echo.Context {
	engine := echo.New()
	request := httptest.NewRequest(method, path, nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	context := engine.NewContext(request, recorder)
	context.SetPath(path)
	return context
}
