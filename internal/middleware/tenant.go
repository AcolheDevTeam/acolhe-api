package middleware

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// Tenant lê as claims injetadas por Auth, resolve a identidade tipada (orgId,
// userId, role) e a coloca no context.Context. A partir daqui o handler nunca
// precisa conhecer a lógica de tenant — os services extraem o orgId via
// tenant.OrgID(ctx) (spec §6).
//
// NOTA Fase 5: o SET LOCAL acolhe.* por transação (RLS como 2ª camada) será
// integrado aqui, abrindo uma tx por requisição e propagando o orgId ao Postgres.
func Tenant() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if publicPaths[c.Path()] {
				return next(c)
			}

			claims, ok := claimsFrom(c.Request().Context())
			if !ok {
				return echo.NewHTTPError(http.StatusUnauthorized, "não autenticado")
			}

			userID, err := uuid.Parse(claims.UserID)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "identidade inválida")
			}

			id := tenant.Identity{UserID: userID, Role: claims.Role}
			if claims.OrganizationID != "" {
				orgID, err := uuid.Parse(claims.OrganizationID)
				if err != nil {
					return echo.NewHTTPError(http.StatusUnauthorized, "organização inválida")
				}
				id.OrgID = orgID
			}

			ctx := tenant.WithIdentity(c.Request().Context(), id)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}
