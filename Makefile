.PHONY: run build tidy dev generate migrate apply lint dev-schema seed db-up

# ---------- App ----------

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

tidy:
	go mod tidy

seed:
	go run ./cmd/seed

# Hot reload em .go e .sql (ver .air.toml). Requer `air` instalado.
dev:
	air

# ---------- Dados: sqlc + Atlas (schema.sql é a fonte da verdade) ----------

# Regenera os tipos Go a partir de internal/db/queries + internal/db/schema.sql.
generate:
	sqlc generate

# Atlas detecta o diff entre schema.sql e as migrations e gera uma nova migration.
# Uso: make migrate name=add_checkin   (requer Docker para o --dev-url)
migrate:
	atlas migrate diff $(name) \
	  --dir "file://internal/db/migrations" \
	  --to "file://internal/db/schema.sql" \
	  --dev-url "docker://postgres/16/dev?search_path=public"

# Aplica as migrations pendentes no banco apontado por DATABASE_URL.
apply:
	atlas migrate apply \
	  --dir "file://internal/db/migrations" \
	  --url "$(DATABASE_URL)"

# Lint de segurança das migrations (roda no CI — justificativa LGPD §3.4).
lint:
	atlas migrate lint \
	  --dir "file://internal/db/migrations" \
	  --dev-url "docker://postgres/16/dev?search_path=public" \
	  --latest 1

# Fluxo normal de desenvolvimento: gera migration + regenera tipos Go.
dev-schema: migrate generate

# ---------- Infra local ----------

# Sobe Postgres + Redis (ver docker-compose.yml).
db-up:
	docker compose up -d
