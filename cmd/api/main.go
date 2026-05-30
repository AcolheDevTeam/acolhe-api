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

	application := app.New(pool, queries, redis, cfg.JWTSecret)

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
