// Package tenant carrega a identidade do requisitante (org, usuário, papel) no
// context.Context. Não conhece Echo nem HTTP — pode ser importado por services e
// middlewares sem acoplar a camada web. É o ponto único onde o orgId multi-tenant
// entra e sai do contexto (spec §6).
package tenant

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNoTenant indica que o contexto não carrega identidade — bug de wiring de
// middleware ou rota pública sendo tratada como autenticada.
var ErrNoTenant = errors.New("identidade ausente no contexto")

// Identity é o conjunto mínimo extraído do JWT pelo middleware de auth.
type Identity struct {
	UserID uuid.UUID
	Role   string
	OrgID  uuid.UUID // uuid.Nil para platform_admin (sem organização)
}

type ctxKey struct{}

var identityKey ctxKey

// WithIdentity devolve um contexto derivado carregando a identidade.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// FromContext recupera a identidade injetada pelo middleware.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey).(Identity)
	return id, ok
}

// OrgID é o atalho usado pelos services para o isolamento multi-tenant.
func OrgID(ctx context.Context) (uuid.UUID, error) {
	id, ok := FromContext(ctx)
	if !ok {
		return uuid.Nil, ErrNoTenant
	}
	return id.OrgID, nil
}
