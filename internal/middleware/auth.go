// Package middleware reúne os middlewares HTTP transversais: autenticação,
// tenant e auditoria. Único lugar (além dos handlers) que conhece o Echo.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/joycesilva/acolhe-api/internal/auth"
)

// publicPaths não exigem JWT (login emite o token; health é probe de liveness).
var publicPaths = map[string]bool{
	"/health":                                true,
	"/login":                                 true,
	"/onboarding/invitations/:token":         true,
	"/onboarding/invitations/:token/accept":  true,
	"/onboarding/invitations/:token/decline": true,
}

// claimsContextKey guarda as claims cruas para o middleware de tenant consumir.
type claimsContextKey struct{}

var claimsKey claimsContextKey

// Auth valida o Bearer token e injeta as claims no contexto da requisição.
// Rotas públicas (login, health) são liberadas pelo skipper interno.
func Auth(secret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if publicPaths[c.Path()] {
				return next(c)
			}

			header := c.Request().Header.Get("Authorization")
			token := strings.TrimPrefix(header, "Bearer ")
			if token == "" || token == header {
				return echo.NewHTTPError(http.StatusUnauthorized, "não autenticado")
			}

			claims, err := auth.ParseToken(secret, token)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "sessão inválida")
			}

			ctx := withClaims(c.Request().Context(), claims)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

func withClaims(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

func claimsFrom(ctx context.Context) (*auth.Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*auth.Claims)
	return c, ok
}
