# acolhe-api

API em Go + PostgreSQL do Acolhe (SaaS clínico para psicólogos, LGPD).

## Stack (ver `acolhe-api-spec.md`)
- **Go** — binário único, tipagem forte
- **Echo** — roteamento; `c.Bind`/`c.JSON` (acoplamento restrito aos handlers)
- **PostgreSQL** — multi-tenant, RLS como 2ª camada
- **Atlas** — schema declarativo (`internal/db/schema.sql`) → migrations + lint
- **sqlc** — SQL puro → tipos Go (o "repository"); código gerado versionado
- **asynq + Redis** — jobs assíncronos (export LGPD, lembretes, PDF) — workers na Fase 6
- **Air** — hot reload em `.go` e `.sql`

## Arquitetura — domain-driven flat
Cada domínio é um package autocontido com `handler.go` (só conhece Echo + `*Service`)
e `service.go` (só conhece `*db.Queries`, nunca o Echo).

```
cmd/api/             entrypoint: pool → db.New → queue.Connect → app.New → Start
internal/
  app/               monta domínios, middlewares e rotas
  account/           identidade: /login, /me
  patient/           pacientes
  session/           sessões clínicas + timeline (regra de vínculo ativo)
  appointment/       agenda (regra de conflito de horário)
  activity/          templates e atribuições de atividades
  document/          documentos (PDF assíncrono — Fase 6)
  checkin/           check-ins (esqueleto — Fase 9)
  notification/      notificações (enfileira via asynq — Fase 6)
  middleware/        auth (JWT), tenant (orgId no context)
  tenant/            identidade no context.Context (sem Echo)
  database/          pool pgx
  queue/             cliente asynq
  db/
    schema.sql       FONTE DA VERDADE (Atlas + sqlc leem daqui)
    queries/         queries-fonte do sqlc (organization_id explícito)
    generated/       código gerado pelo sqlc — versionado
    migrations/      gerado pelo Atlas
```

## Docker local (Colima — sem Docker Desktop)
Atlas (`docker://`) e os testes com testcontainers precisam de um daemon Docker.
Usamos **Colima** (CLI, sem GUI/licença):
```bash
brew install colima docker docker-compose   # uma vez
colima start --cpu 2 --memory 4              # sobe a VM (daemon Docker)
```
Ferramentas que usam o SDK do Docker (Atlas, testcontainers) precisam destas
variáveis — adicione ao seu `~/.zshrc`:
```bash
export DOCKER_HOST="unix://$HOME/.colima/default/docker.sock"
export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE="/var/run/docker.sock"
```
> Nota: `atlas migrate lint` é gratuito apenas até a v0.37 (a partir da v0.38 exige
> `atlas login`). O projeto fixa o Atlas **v0.31.0** community para manter o lint no CI sem conta.

## Primeiros passos
```bash
cp .env.example .env
colima start        # daemon Docker (ver seção acima)
make db-up          # sobe Postgres + Redis (docker compose)
make apply          # aplica migrations (Atlas; precisa de DATABASE_URL)
make generate       # regenera tipos do sqlc (já versionados)
make seed           # cria org/psicólogo/pacientes de exemplo
make dev            # API com hot reload (air)  — ou `make run`
```

`GET /health` confirma a aplicação e a conexão com o banco.

## Fluxo de schema
```
internal/db/schema.sql ──► Atlas ──► internal/db/migrations/   (como o banco evolui)
internal/db/schema.sql ──► sqlc  ──► internal/db/generated/    (como o código acessa)
```
- `make migrate name=<nome>` — Atlas detecta o diff e gera a migration (requer Docker p/ o dev-url)
- `make lint` — lint de segurança das migrations (roda no CI; bloqueia drops destrutivos)
- `make dev-schema` — `migrate` + `generate` juntos

## Multi-tenant
`organization_id` é **explícito** em toda query. Tabelas que não carregam a coluna
(`session`, `appointment`, `patient_relationship`) fazem o isolamento via join em
`patient_profile`. O `middleware/tenant.go` injeta o `orgId` no `context.Context`;
o service o extrai com `tenant.OrgID(ctx)` — o handler nunca conhece a lógica de tenant.

## Notas de segurança (do ERD)
- **RLS** é a 2ª camada. A integração do `SET LOCAL acolhe.*` por transação está
  prevista para a Fase 5 (middleware de tenant).
- `documentary_record` é **só do psicólogo autor**.
- `audit_log` e `consent` são **append-only**.
- Campos `*_encrypted` recebem criptografia a nível de coluna via KMS (pendente).

## Status do refactor (spec v1.0)
Concluído e verificado (build/vet/test + integração com Docker):
- **Fases 1–4** — sqlc, Atlas, Echo, domain-driven flat.
- **Fase 5** — middlewares dedicados: `auth` (JWT), `tenant` (orgId no context),
  `audit` (audit_log assíncrono), `tx` (transação por requisição + `SET LOCAL
  acolhe.*` para RLS como 2ª camada).
- **Fase 6** — jobs assíncronos (asynq + Redis): pacote `tasks` (contrato),
  workers `lgpd:export` / `notification:reminder` / `document:pdf` em
  `internal/worker`, binário `cmd/worker`, e produtores ligados
  (`notification` enfileira reminder; `document` cria o registro e enfileira pdf).
- **Fase 9** — cobertura dos domínios: check-in (tabela nova via migration Atlas
  incremental + create/list), respostas de atividade (submit/list), e trigger de
  exportação LGPD (`POST /patients/:id/export` → enfileira `lgpd:export`).
- **Fase 8 (parcial)** — testes de handler (httptest) + integração (testcontainers
  e Redis real): isolamento multi-tenant, stack completo (tx + audit), round-trip
  de fila.

Pendente (refinamentos): valores tipados de resposta (`activity_response_value`),
render real de PDF (infra/S3), listagem de templates de documento e de notificações.

> No deploy, migrations usam o dono do schema e API/worker usam `acolhe_app`,
> um role `NOSUPERUSER NOBYPASSRLS` configurado idempotentemente antes das
> migrations. O middleware aplica o contexto `acolhe.*` em cada transação.
