// Comando worker: consome as tarefas assíncronas do Redis (asynq) — exportação
// LGPD, lembretes e geração de PDF. Roda como processo separado da API.
//
//	go run ./cmd/worker
package main

import (
	"context"
	"log"

	"github.com/hibiken/asynq"

	"github.com/joycesilva/acolhe-api/internal/config"
	"github.com/joycesilva/acolhe-api/internal/database"
	db "github.com/joycesilva/acolhe-api/internal/db/generated"
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

	mux := asynq.NewServeMux()
	worker.New(db.New(pool)).Register(mux)

	log.Println("acolhe-worker consumindo tarefas do Redis")
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker: %v", err)
	}
}
