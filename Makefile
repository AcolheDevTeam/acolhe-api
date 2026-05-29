.PHONY: run build tidy sqlc migrate-up db-up

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

tidy:
	go mod tidy

# Gera o código de acesso a dados a partir de migrations/ e query/
sqlc:
	sqlc generate

# Sobe um Postgres local de desenvolvimento via Docker
db-up:
	docker run --rm -d --name acolhe-db \
	  -e POSTGRES_USER=acolhe -e POSTGRES_PASSWORD=acolhe -e POSTGRES_DB=acolhe \
	  -p 5432:5432 postgres:16

# Aplica todas as migrations em ordem (requer psql e DATABASE_URL)
migrate-up:
	@for f in migrations/*.sql; do echo "==> $$f"; psql "$$DATABASE_URL" -f "$$f"; done
