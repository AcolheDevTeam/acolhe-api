package tenant

import (
	"context"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

// O middleware de tenant abre uma transação por requisição, seta o contexto RLS
// (acolhe.*) e injeta aqui as queries tx-scoped. Os services as recuperam com
// Queries(ctx, fallback): assim o mesmo código roda dentro da tx (com RLS como 2ª
// camada) em produção e com o querier injetado direto nos testes unitários.

type queriesKey struct{}

// WithQueries injeta um db.Querier (tx-scoped) no contexto.
func WithQueries(ctx context.Context, q db.Querier) context.Context {
	return context.WithValue(ctx, queriesKey{}, q)
}

// Queries devolve o querier tx-scoped do contexto, ou o fallback (singleton)
// quando não há transação por requisição (ex.: rota pública ou teste unitário).
func Queries(ctx context.Context, fallback db.Querier) db.Querier {
	if q, ok := ctx.Value(queriesKey{}).(db.Querier); ok && q != nil {
		return q
	}
	return fallback
}
