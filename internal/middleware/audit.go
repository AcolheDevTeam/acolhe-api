package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// Audit grava uma entrada em audit_log para cada mutação bem-sucedida (POST/PUT/
// PATCH/DELETE com status 2xx). A escrita é assíncrona e em um contexto próprio
// (desacoplado da requisição), pois audit_log é append-only e deve sobreviver
// mesmo a um rollback da transação da requisição (spec §4.3, Fase 5).
//
// Escreve via o querier-base (não o tx-scoped): o log de auditoria não pode ser
// desfeito junto com a operação auditada.
func Audit(q db.Querier) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			err := next(c)

			if !isMutation(c.Request().Method) {
				return err
			}
			status := c.Response().Status
			if err != nil || status < 200 || status >= 300 {
				return err // só auditamos mutações que de fato persistiram
			}

			id, ok := tenant.FromContext(c.Request().Context())
			if !ok {
				return err
			}

			entry := db.WriteAuditLogParams{
				ActorUserID:  id.UserID,
				Action:       c.Request().Method,
				ResourceType: resourceType(c.Path()),
				ResourceID:   c.Param("id"),
			}
			if id.OrgID != uuid.Nil {
				org := id.OrgID
				entry.OrganizationID = &org
			}

			// Desacoplado da requisição: contexto próprio com timeout curto.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = q.WriteAuditLog(ctx, entry)
			}()

			return err
		}
	}
}

func isMutation(method string) bool {
	switch method {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

// resourceType extrai o primeiro segmento da rota (ex.: "/sessions/:id" -> "sessions").
func resourceType(path string) string {
	p := strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	if p == "" {
		return "root"
	}
	return p
}
