package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// HashPassword gera um hash bcrypt da senha em texto puro.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword compara senha em texto puro com o hash armazenado.
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// Claims carregadas no token de sessão.
type Claims struct {
	UserID         string `json:"uid"`
	Role           string `json:"role"`
	OrganizationID string `json:"org,omitempty"`
	jwt.RegisteredClaims
}

var allowedRoles = map[string]bool{
	"platform_admin": true,
	"org_admin":      true,
	"psychologist":   true,
	"patient":        true,
}

// GenerateToken emite um JWT HS256 válido por 7 dias.
func GenerateToken(secret, userID, role, orgID string) (string, error) {
	if err := validateIdentityClaims(secret, userID, role, orgID); err != nil {
		return "", err
	}
	now := time.Now()
	claims := Claims{
		UserID:         userID,
		Role:           role,
		OrganizationID: orgID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// ParseToken valida e decodifica o JWT.
func ParseToken(secret, tokenStr string) (*Claims, error) {
	if secret == "" || tokenStr == "" {
		return nil, errors.New("token inválido")
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return nil, errors.New("token inválido")
	}
	if claims.Subject != claims.UserID {
		return nil, errors.New("token inválido")
	}
	if err := validateIdentityClaims(
		secret,
		claims.UserID,
		claims.Role,
		claims.OrganizationID,
	); err != nil {
		return nil, errors.New("token inválido")
	}
	return claims, nil
}

func validateIdentityClaims(secret, userID, role, orgID string) error {
	if secret == "" {
		return errors.New("segredo JWT ausente")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return errors.New("usuário JWT inválido")
	}
	if !allowedRoles[role] {
		return errors.New("papel JWT inválido")
	}
	if role == "platform_admin" {
		if orgID != "" {
			return errors.New("platform admin não pertence a organização")
		}
		return nil
	}
	if _, err := uuid.Parse(orgID); err != nil {
		return errors.New("organização JWT inválida")
	}
	return nil
}
