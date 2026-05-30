// Package account cobre identidade e sessão de autenticação: login e /me.
// A spec §4.3 não lista um domínio de auth, mas os endpoints /login e /me já
// existem e precisam de um lar — este é o domínio de identidade, no mesmo padrão
// flat (handler + service) dos demais.
package account

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/joycesilva/acolhe-api/internal/auth"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// ErrInvalidCredentials cobre tanto e-mail inexistente quanto senha errada —
// nunca revelamos qual dos dois (evita enumeração de usuários).
var ErrInvalidCredentials = errors.New("credenciais inválidas")

// ErrUserNotFound ocorre quando o token é válido mas o usuário sumiu.
var ErrUserNotFound = errors.New("usuário não encontrado")

type Service struct {
	q      db.Querier
	secret string
}

func NewService(q db.Querier, jwtSecret string) *Service {
	return &Service{q: q, secret: jwtSecret}
}

// User é a projeção pública de um usuário (sem hash de senha).
type User struct {
	ID             uuid.UUID  `json:"id"`
	Email          string     `json:"email"`
	Role           string     `json:"role"`
	OrganizationID *uuid.UUID `json:"organizationId"`
}

// LoginResult carrega o token emitido e o usuário autenticado.
type LoginResult struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// Login valida credenciais e emite um JWT de 7 dias.
func (s *Service) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	u, err := tenant.Queries(ctx, s.q).GetUserByEmail(ctx, email)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if !auth.CheckPassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}

	org := ""
	if u.OrganizationID != nil {
		org = u.OrganizationID.String()
	}
	token, err := auth.GenerateToken(s.secret, u.ID.String(), u.Role, org)
	if err != nil {
		return nil, err
	}

	return &LoginResult{
		Token: token,
		User:  User{ID: u.ID, Email: u.Email, Role: u.Role, OrganizationID: u.OrganizationID},
	}, nil
}

// Me resolve o usuário da identidade no contexto.
func (s *Service) Me(ctx context.Context) (*User, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrUserNotFound
	}
	u, err := tenant.Queries(ctx, s.q).GetUserByID(ctx, id.UserID)
	if err != nil {
		return nil, ErrUserNotFound
	}
	return &User{ID: u.ID, Email: u.Email, Role: u.Role, OrganizationID: u.OrganizationID}, nil
}
