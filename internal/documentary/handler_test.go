package documentary

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/joycesilva/acolhe-api/internal/tenant"
)

func TestUnavailableConfigurationAndRoleBoundary(t *testing.T) {
	for _, tc := range []struct {
		role   string
		status int
	}{{"psychologist", 503}, {"patient", 403}, {"org_admin", 403}, {"platform_admin", 403}} {
		t.Run(tc.role, func(t *testing.T) {
			e := echo.New()
			NewHandler(NewService(nil, nil)).Register(e)
			req := httptest.NewRequest("GET", "/documentary/patients", nil)
			req = req.WithContext(tenant.WithIdentity(req.Context(), tenant.Identity{UserID: uuid.New(), OrgID: uuid.New(), Role: tc.role}))
			response := httptest.NewRecorder()
			e.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			if response.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("missing private cache policy")
			}
		})
	}
}
