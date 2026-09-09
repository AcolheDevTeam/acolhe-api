package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// TenantTx abre uma transação por requisição autenticada, propaga a identidade
// ao Postgres via set_config('acolhe.*', ..., true) e injeta um db.Querier
// tx-scoped no contexto. Isso ativa o RLS como 2ª camada de defesa — a 1ª é o
// organization_id explícito nas queries (spec §6).
//
// Quando pool é nil (testes unitários com FakeQuerier) o middleware é um no-op:
// os services caem no querier injetado via tenant.Queries(ctx, s.q).
//
// CAVEAT (MVP): o commit ocorre após next(c). Como o Echo escreve a resposta
// dentro do handler (c.JSON), no caminho de sucesso o corpo pode ser enviado
// antes do commit. Falha de commit é rara (constraint já falharia no INSERT) e
// fica logada; revisitar com buffer de resposta ou Response().Before se preciso.
func TenantTx(pool *pgxpool.Pool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if pool == nil || isPublicPath(c.Path()) {
				return next(c)
			}
			id, ok := tenant.FromContext(c.Request().Context())
			if !ok {
				return next(c)
			}

			ctx := c.Request().Context()
			tx, err := pool.Begin(ctx)
			if err != nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "indisponível")
			}
			committed := false
			defer func() {
				if !committed {
					_ = tx.Rollback(ctx)
				}
			}()

			txq := db.New(tx)
			if err := applyRLSContext(ctx, tx, txq, id); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "falha no contexto de tenant")
			}

			c.SetRequest(c.Request().WithContext(tenant.WithQueries(ctx, txq)))

			if err := next(c); err != nil {
				return err // rollback no defer
			}
			if err := tx.Commit(ctx); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "falha ao confirmar transação")
			}
			committed = true
			return nil
		}
	}
}

// applyRLSContext seta as variáveis de sessão lidas pelas policies de RLS.
func applyRLSContext(ctx context.Context, tx pgx.Tx, txq *db.Queries, id tenant.Identity) error {
	if err := setConfig(ctx, tx, "acolhe.user_id", id.UserID.String()); err != nil {
		return err
	}
	if err := setConfig(ctx, tx, "acolhe.user_role", id.Role); err != nil {
		return err
	}
	if id.OrgID != uuid.Nil {
		if err := setConfig(ctx, tx, "acolhe.organization_id", id.OrgID.String()); err != nil {
			return err
		}
	}
	// psychologist_id é exigido pela policy patient_isolation p/ role psychologist.
	if id.Role == "psychologist" {
		if psy, err := txq.GetPsychologistByUser(ctx, id.UserID); err == nil {
			if err := setConfig(ctx, tx, "acolhe.psychologist_id", psy.ID.String()); err != nil {
				return err
			}
		}
	}
	return nil
}

func setConfig(ctx context.Context, tx pgx.Tx, key, val string) error {
	_, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", key, val)
	return err
}
