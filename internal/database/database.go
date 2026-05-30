// Package database abre e valida o pool de conexões PostgreSQL.
// Separado do pacote db (gerado pelo sqlc) para que o sqlc seja a única coisa
// dentro de internal/db.
package database

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect abre um pool de conexões PostgreSQL e confirma a conectividade.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
