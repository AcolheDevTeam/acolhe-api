// Package account cobre identidade e sessão de autenticação: login e /me.
// A spec §4.3 não lista um domínio de auth, mas os endpoints /login e /me já
// existem e precisam de um lar — este é o domínio de identidade, no mesmo padrão
// flat (handler + service) dos demais.
package account

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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
	pool   *pgxpool.Pool
}

func NewService(q db.Querier, jwtSecret string, pools ...*pgxpool.Pool) *Service {
	var pool *pgxpool.Pool
	if len(pools) > 0 {
		pool = pools[0]
	}
	return &Service{q: q, secret: jwtSecret, pool: pool}
}

// User é a projeção pública de um usuário (sem hash de senha).
type User struct {
	ID             uuid.UUID       `json:"id"`
	Email          string          `json:"email"`
	Role           string          `json:"role"`
	OrganizationID *uuid.UUID      `json:"organizationId"`
	Patient        *PatientContext `json:"patient,omitempty"`
}

type PatientContext struct {
	ID                 uuid.UUID `json:"id"`
	FullName           string    `json:"fullName"`
	RelationshipStatus string    `json:"relationshipStatus"`
	Consented          bool      `json:"consented"`
}

// LoginResult carrega o token emitido e o usuário autenticado.
type LoginResult struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

const signupTermsVersion = "0.3"

var signupEmailPattern = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

var validCRPRegions = map[string]struct{}{
	"01": {}, "02": {}, "03": {}, "04": {}, "05": {}, "06": {},
	"07": {}, "08": {}, "09": {}, "10": {}, "11": {}, "12": {},
	"13": {}, "14": {}, "15": {}, "16": {}, "17": {}, "18": {},
	"19": {}, "20": {}, "21": {}, "22": {}, "23": {}, "24": {},
}

var (
	ErrSignupConflict    = errors.New("cadastro não pôde ser concluído")
	ErrSignupInvalid     = errors.New("dados de cadastro inválidos")
	ErrSignupUnavailable = errors.New("cadastro indisponível")
)

type SignupInput struct {
	Email          string
	Password       string
	FullName       string
	CRPNumber      string
	CRPState       string
	CPF            string
	Approach       string
	AcceptTerms    bool
	AcceptPrivacy  bool
	TermsVersion   string
	PrivacyVersion string
	IPAddress      net.IP
}

type SignupResult struct {
	Token            string    `json:"token"`
	User             User      `json:"user"`
	PsychologistID   uuid.UUID `json:"psychologistId"`
	CRPStatus        string    `json:"crpStatus"`
	OnboardingStatus string    `json:"onboardingStatus"`
	TermsVersion     string    `json:"termsVersion"`
	PrivacyVersion   string    `json:"privacyVersion"`
}

func (s *Service) Signup(ctx context.Context, in SignupInput) (*SignupResult, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.FullName = strings.TrimSpace(in.FullName)
	in.CRPNumber = strings.TrimSpace(in.CRPNumber)
	in.CRPState = normalizeCRPRegion(in.CRPState)
	in.Approach = strings.TrimSpace(in.Approach)
	in.CPF = digitsOnly(in.CPF)
	if !isValidSignupEmail(in.Email) || len(in.Email) > 254 || in.FullName == "" || len(in.FullName) > 200 || !isCRPNumber(in.CRPNumber) || !isValidCRPRegion(in.CRPState) || len(in.Password) < 8 || len(in.Password) > 128 || !in.AcceptTerms || !in.AcceptPrivacy || in.TermsVersion != signupTermsVersion || in.PrivacyVersion != signupTermsVersion {
		return nil, ErrSignupInvalid
	}
	if in.CPF != "" && len(in.CPF) != 11 {
		return nil, ErrSignupInvalid
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, ErrSignupUnavailable
	}
	var cpf []byte
	if in.CPF != "" {
		cpf, err = encryptPII([]byte(in.CPF))
		if err != nil {
			return nil, ErrSignupUnavailable
		}
	}
	if s.pool == nil {
		return nil, ErrSignupUnavailable
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, ErrSignupUnavailable
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orgID := uuid.New()
	userID := uuid.New()
	psyID := uuid.New()
	// Um Exec por comando: o pgx usa o protocolo estendido, que recusa múltiplos
	// comandos num mesmo statement preparado (SQLSTATE 42601). Agrupar os INSERT
	// numa única string fazia todo cadastro falhar. A atomicidade vem da
	// transação, não do agrupamento.
	inserts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organization (id, name, slug, status)
		  VALUES ($1, $2, $3, 'active')`,
			[]any{orgID, "Clínica de " + in.FullName, "psicologo-" + orgID.String()}},
		{`INSERT INTO "user" (id, organization_id, email, password_hash, role, status)
		  VALUES ($1, $2, $3, $4, 'psychologist', 'active')`,
			[]any{userID, orgID, in.Email, hash}},
		{`INSERT INTO psychologist_profile (id, user_id, full_name, crp_number, crp_state, crp_status, approach, cpf_encrypted)
		  VALUES ($1, $2, $3, $4, $5, 'pending', NULLIF($6, ''), $7)`,
			[]any{psyID, userID, in.FullName, in.CRPNumber, in.CRPState, in.Approach, cpf}},
	}
	for _, insert := range inserts {
		if _, err := tx.Exec(ctx, insert.sql, insert.args...); err != nil {
			if isSignupConflict(err) {
				return nil, ErrSignupConflict
			}
			return nil, ErrSignupUnavailable
		}
	}

	consents, err := tx.Exec(ctx, `
		INSERT INTO consent (user_id, document_id, accepted, ip_address)
		SELECT $1, d.id, true, $3
		FROM consent_document d
		WHERE d.scope IN ('terms_of_use', 'privacy_policy') AND d.version = $2`,
		userID, signupTermsVersion, ipValue(in.IPAddress))
	if err != nil {
		if isSignupConflict(err) {
			return nil, ErrSignupConflict
		}
		return nil, ErrSignupUnavailable
	}
	// Os dois documentos da versão corrente precisam existir e ter sido aceitos:
	// sem terms_of_use e privacy_policy o cadastro não tem base legal.
	if consents.RowsAffected() != 2 {
		return nil, ErrSignupUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, ErrSignupUnavailable
	}
	token, err := auth.GenerateToken(s.secret, userID.String(), "psychologist", orgID.String())
	if err != nil {
		return nil, ErrSignupUnavailable
	}
	return &SignupResult{
		Token:          token,
		User:           User{ID: userID, Email: in.Email, Role: "psychologist", OrganizationID: &orgID},
		PsychologistID: psyID, CRPStatus: "pending", OnboardingStatus: "complete",
		TermsVersion: signupTermsVersion, PrivacyVersion: signupTermsVersion,
	}, nil
}

func normalizeCRPRegion(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "CRP-")
	if len(value) == 1 && value[0] >= '0' && value[0] <= '9' {
		value = "0" + value
	}
	return value
}

func isValidCRPRegion(value string) bool {
	_, ok := validCRPRegions[value]
	return ok
}

func isValidSignupEmail(value string) bool {
	if !signupEmailPattern.MatchString(value) || strings.Contains(value, "..") {
		return false
	}
	local := strings.SplitN(value, "@", 2)[0]
	return !strings.HasPrefix(local, ".") && !strings.HasSuffix(local, ".")
}

func isCRPNumber(value string) bool {
	if len(value) < 4 || len(value) > 8 {
		return false
	}
	_, err := strconv.Atoi(value)
	return err == nil
}

func digitsOnly(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ipValue(ip net.IP) any {
	if ip == nil {
		return nil
	}
	parsed, err := netip.ParseAddr(ip.String())
	if err != nil {
		return nil
	}
	return parsed
}

func isSignupConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func encryptPII(plain []byte) ([]byte, error) {
	keyText := os.Getenv("PII_ENCRYPTION_KEY")
	key, err := hex.DecodeString(keyText)
	if err != nil || len(key) != 32 {
		return nil, errors.New("PII_ENCRYPTION_KEY deve ser uma chave hex de 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
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
	user := &User{ID: u.ID, Email: u.Email, Role: u.Role, OrganizationID: u.OrganizationID}
	if u.Role == "patient" {
		row, err := tenant.Queries(ctx, s.q).GetPatientPortalContext(ctx, &id.UserID)
		if err != nil {
			return nil, ErrUserNotFound
		}
		user.Patient = &PatientContext{
			ID: row.ID, FullName: row.FullName,
			RelationshipStatus: row.RelationshipStatus,
			Consented:          row.Consented,
		}
	}
	return user, nil
}
