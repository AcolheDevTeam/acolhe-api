package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joycesilva/acolhe-api/internal/auth"
)

type Server struct {
	pool      *pgxpool.Pool
	jwtSecret string
}

func New(pool *pgxpool.Pool, jwtSecret string) *Server {
	return &Server{pool: pool, jwtSecret: jwtSecret}
}

// Routes monta o roteador com todos os endpoints.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /me", s.requireAuth(s.handleMe))
	mux.HandleFunc("GET /patients", s.requireAuth(s.handlePatients))
	mux.HandleFunc("GET /patients/{id}", s.requireAuth(s.handlePatient))
	return withCORS(mux)
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey string

const claimsKey ctxKey = "claims"

// requireAuth valida o Bearer token e injeta as claims no contexto.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		token := strings.TrimPrefix(h, "Bearer ")
		if token == "" || token == h {
			writeErr(w, http.StatusUnauthorized, "não autenticado")
			return
		}
		claims, err := auth.ParseToken(s.jwtSecret, token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "sessão inválida")
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next(w, r.WithContext(ctx))
	}
}

func claimsFrom(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(claimsKey).(*auth.Claims)
	return c
}

// ---------- handlers ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "db indisponível")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type meResponse struct {
	ID             string  `json:"id"`
	Email          string  `json:"email"`
	Role           string  `json:"role"`
	OrganizationID *string `json:"organizationId"`
}

type loginResponse struct {
	Token string     `json:"token"`
	User  meResponse `json:"user"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "corpo inválido")
		return
	}

	var (
		id, email, role, passwordHash string
		orgID                         *string
	)
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, email, role, password_hash, organization_id
		 FROM "user" WHERE lower(email) = lower($1) AND status = 'active'`,
		req.Email,
	).Scan(&id, &email, &role, &passwordHash, &orgID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "credenciais inválidas")
		return
	}

	if !auth.CheckPassword(passwordHash, req.Password) {
		writeErr(w, http.StatusUnauthorized, "credenciais inválidas")
		return
	}

	org := ""
	if orgID != nil {
		org = *orgID
	}
	token, err := auth.GenerateToken(s.jwtSecret, id, role, org)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "falha ao gerar sessão")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token: token,
		User:  meResponse{ID: id, Email: email, Role: role, OrganizationID: orgID},
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r.Context())
	var (
		id, email, role string
		orgID           *string
	)
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, email, role, organization_id FROM "user" WHERE id = $1`,
		c.UserID,
	).Scan(&id, &email, &role, &orgID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "usuário não encontrado")
		return
	}
	writeJSON(w, http.StatusOK, meResponse{ID: id, Email: email, Role: role, OrganizationID: orgID})
}

type patient struct {
	ID        string `json:"id"`
	FullName  string `json:"fullName"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

func (s *Server) handlePatients(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, full_name, status, created_at
		 FROM patient_profile
		 WHERE organization_id = $1 AND status <> 'deleted'
		 ORDER BY full_name`,
		c.OrganizationID,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "erro ao listar pacientes")
		return
	}
	defer rows.Close()

	out := []patient{}
	for rows.Next() {
		var p patient
		var createdAt time.Time
		if err := rows.Scan(&p.ID, &p.FullName, &p.Status, &createdAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "erro ao ler pacientes")
			return
		}
		p.CreatedAt = createdAt.Format(time.RFC3339)
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePatient(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r.Context())
	id := r.PathValue("id")
	var p patient
	var createdAt time.Time
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, full_name, status, created_at
		 FROM patient_profile
		 WHERE id = $1 AND organization_id = $2 AND status <> 'deleted'`,
		id, c.OrganizationID,
	).Scan(&p.ID, &p.FullName, &p.Status, &createdAt)
	if err != nil {
		writeErr(w, http.StatusNotFound, "paciente não encontrado")
		return
	}
	p.CreatedAt = createdAt.Format(time.RFC3339)
	writeJSON(w, http.StatusOK, p)
}
