.PHONY: run build tidy dev generate migrate apply lint dev-schema seed golangci sec trivy semgrep check

# ---------- App ----------

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

tidy:
	go mod tidy

seed:
	go run ./cmd/seed

# Sobe API, Postgres e Redis; migrations e seed rodam automaticamente.
dev:
	docker compose up

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

# ---------- Qualidade / análise estática (ver .github/workflows/ci.yml) ----------

# Análise estática agregada de Go (inclui gosec). Requer golangci-lint instalado:
#   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
# Obs: o alvo `lint` acima é o lint de migrations (Atlas); este é o de código Go.
golangci:
	golangci-lint run ./...

# Apenas o scanner de segurança gosec (subset do que o lint roda).
sec:
	golangci-lint run --no-config --default=none --enable=gosec ./...

# Scanner de vulnerabilidades, misconfigs e secrets (requer trivy instalado).
trivy:
	trivy fs --scanners vuln,misconfig,secret --severity CRITICAL,HIGH --ignore-unfixed .

# Regras Semgrep de isolamento multi-tenant (requer semgrep instalado).
semgrep:
	semgrep --config .semgrep/ --error

# Roda o pacote completo de quality gate localmente.
check: golangci trivy semgrep
	go build ./... && go test ./...
