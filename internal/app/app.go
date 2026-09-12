// Package app monta a aplicação: cria o Echo, registra middlewares globais e
// pendura cada domínio (spec §4.5). É o único lugar que conhece todos os
// domínios ao mesmo tempo.
package app

import (
	"context"
	"net/http"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	emw "github.com/labstack/echo/v4/middleware"

	"github.com/joycesilva/acolhe-api/internal/account"
	"github.com/joycesilva/acolhe-api/internal/activity"
	"github.com/joycesilva/acolhe-api/internal/appointment"
	"github.com/joycesilva/acolhe-api/internal/checkin"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/document"
	"github.com/joycesilva/acolhe-api/internal/mailer"
	"github.com/joycesilva/acolhe-api/internal/middleware"
	"github.com/joycesilva/acolhe-api/internal/notification"
	"github.com/joycesilva/acolhe-api/internal/onboarding"
	"github.com/joycesilva/acolhe-api/internal/patient"
	"github.com/joycesilva/acolhe-api/internal/session"
)

// App embrulha o servidor Echo já montado.
type App struct {
	echo *echo.Echo
}

// New monta domínios, rotas e middlewares.
//
// pool pode ser nil (testes unitários com FakeQuerier): nesse caso o middleware
// TenantTx vira no-op e os services usam o querier injetado direto.
// Option ajusta integrações opcionais da aplicação (ex.: e-mail de convite).
type Option func(*options)

type options struct {
	patient []patient.Option
}

// WithInvitationMailer liga o envio de convites de pacientes por e-mail.
func WithInvitationMailer(m mailer.Mailer, frontendURL string) Option {
	return func(o *options) {
		o.patient = append(o.patient, patient.WithInvitationMailer(m, frontendURL))
	}
}

func New(pool *pgxpool.Pool, q db.Querier, queue *asynq.Client, jwtSecret string, opts ...Option) *App {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	e := echo.New()
	e.HideBanner = true

	// CORS substitui o withCORS manual do protótipo (spec §3.2).
	e.Use(emw.CORSWithConfig(emw.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowHeaders: []string{echo.HeaderAuthorization, echo.HeaderContentType},
		AllowMethods: []string{
			http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
		},
	}))

	// Pipeline (spec §4.2): Auth (valida JWT) → Tenant (orgId no context) →
	// Audit (log assíncrono pós-commit) → TenantTx (tx + SET LOCAL acolhe.* p/ RLS).
	// Audit é externo a TenantTx para enxergar o status final já comitado.
	e.Use(middleware.Auth(jwtSecret))
	e.Use(middleware.Tenant())
	e.Use(middleware.Audit(q))
	e.Use(middleware.TenantTx(pool))

	// Health: probe de liveness + conectividade com o banco.
	e.GET("/health", func(c echo.Context) error {
		if _, err := q.HealthCheck(c.Request().Context()); err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "db indisponível")
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Domínios (mesmo padrão handler+service para todos).
	account.NewHandler(account.NewService(q, jwtSecret, pool)).Register(e)
	onboarding.NewHandler(onboarding.NewService(q)).Register(e)
	patient.NewHandler(patient.NewService(q, queue, o.patient...)).Register(e)
	session.NewHandler(session.NewService(q)).Register(e)
	appointment.NewHandler(appointment.NewService(q)).Register(e)
	activity.NewHandler(activity.NewService(q)).Register(e)
	document.NewHandler(document.NewService(q, queue)).Register(e)
	checkin.NewHandler(checkin.NewService(q)).Register(e)
	notification.NewHandler(notification.NewService(queue)).Register(e)

	return &App{echo: e}
}

// Start sobe o servidor HTTP na porta informada (ex.: ":8080").
func (a *App) Start(addr string) error {
	return a.echo.Start(addr)
}

// Handler expõe o roteador como http.Handler (usado em testes com httptest).
func (a *App) Handler() http.Handler {
	return a.echo
}

// Shutdown encerra o servidor graciosamente.
func (a *App) Shutdown(ctx context.Context) error {
	return a.echo.Shutdown(ctx)
}
