package middleware

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

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
			if pool == nil || publicPaths[c.Path()] {
				return next(c)
			}
			id, ok := tenant.FromContext(c.Request().Context())
			if !ok {
				return next(c)
			}

			ctx := c.Request().Context()
			tx, txq, err := tenant.BeginTransaction(ctx, pool, id)
			if err != nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "indisponível")
			}
			committed := false
			defer func() {
				if !committed {
					_ = tx.Rollback(ctx)
				}
			}()

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
