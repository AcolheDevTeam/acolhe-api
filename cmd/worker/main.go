// Comando worker: consome as tarefas assíncronas do Redis (asynq) — exportação
// LGPD, lembretes e geração de PDF. Roda como processo separado da API.
//
//	go run ./cmd/worker
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/hibiken/asynq"

	"github.com/joycesilva/acolhe-api/internal/config"
	"github.com/joycesilva/acolhe-api/internal/database"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/exporter"
	"github.com/joycesilva/acolhe-api/internal/worker"
)

func main() {
	cfg := config.Load()

	pool, err := database.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr},
		asynq.Config{
			Concurrency: 10,
			Queues:      map[string]int{"default": 1},
		},
	)

	objectStore, err := exporter.NewS3ObjectStore(exporter.S3Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
		AccessKeyID: cfg.S3AccessKeyID, SecretKey: cfg.S3SecretKey,
		SessionToken: cfg.S3SessionToken,
	}, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		log.Fatalf("s3: %v", err)
	}
	mailer, err := exporter.NewSMTPMailer(exporter.SMTPConfig{
		Address: cfg.SMTPAddress, Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword, From: cfg.SMTPFrom,
	})
	if err != nil {
		log.Fatalf("smtp: %v", err)
	}

	mux := asynq.NewServeMux()
	worker.New(
		db.New(pool),
		worker.WithLGPDExport(
			worker.NewPostgresLGPDExportRepository(pool),
			objectStore,
			mailer,
		),
	).Register(mux)

	log.Println("acolhe-worker consumindo tarefas do Redis")
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker: %v", err)
	}
}
