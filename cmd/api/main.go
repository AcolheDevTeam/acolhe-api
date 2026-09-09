package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joycesilva/acolhe-api/internal/app"
	"github.com/joycesilva/acolhe-api/internal/config"
	"github.com/joycesilva/acolhe-api/internal/database"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/mailer"
	"github.com/joycesilva/acolhe-api/internal/queue"
)

func main() {
	cfg := config.Load()

	// pool → db.New(pool) → queue.Connect() → app.New(...) → app.Start (spec §4.5).
	pool, err := database.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	queries := db.New(pool)

	redis := queue.Connect(cfg.RedisAddr)
	defer func() { _ = redis.Close() }()

	var appOptions []app.Option
	if cfg.SMTPAddress != "" {
		smtpMailer, err := mailer.NewSMTP(mailer.Config{
			Address: cfg.SMTPAddress, Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword, From: cfg.SMTPFrom,
		})
		if err != nil {
			log.Fatalf("smtp: %v", err)
		}
		appOptions = append(appOptions, app.WithInvitationMailer(smtpMailer, cfg.FrontendURL))
		log.Printf("convites por e-mail habilitados via %s (links em %s)", cfg.SMTPAddress, cfg.FrontendURL)
	} else {
		log.Printf("SMTP_ADDRESS não configurado: convites ficam apenas com link copiável")
	}

	application := app.New(pool, queries, redis, cfg.JWTSecret, appOptions...)

	go func() {
		log.Printf("acolhe-api ouvindo em :%s", cfg.Port)
		if err := application.Start(":" + cfg.Port); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := application.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	log.Println("encerrado")
}
