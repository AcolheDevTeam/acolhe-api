# acolhe-api

API em Go + PostgreSQL do Acolhe (SaaS clínico para psicólogos, LGPD).

## Stack
- Go (stdlib `net/http`)
- PostgreSQL com Row-Level Security
- [pgx](https://github.com/jackc/pgx) para acesso ao banco
- [sqlc](https://sqlc.dev) para gerar código a partir de SQL

## Estrutura
```
cmd/api/          entrypoint (servidor HTTP + /health)
internal/config/  carregamento de configuração via env
internal/db/      pool de conexões pgx
internal/store/   código gerado pelo sqlc (não versionado)
migrations/       schema SQL versionado (0001_init, 0002_rls)
query/            queries-fonte para o sqlc
```

## Primeiros passos
```bash
cp .env.example .env
make db-up          # sobe Postgres local (Docker)
make migrate-up     # aplica migrations (precisa de DATABASE_URL no ambiente)
make tidy           # baixa dependências
make run            # sobe a API em :8080
```

`GET /health` confirma a aplicação e a conexão com o banco.

## Notas de segurança (do ERD)
- **RLS** é a 2ª camada de defesa. A aplicação seta o contexto por transação:
  ```sql
  SET LOCAL acolhe.user_id = '...';
  SET LOCAL acolhe.organization_id = '...';
  SET LOCAL acolhe.psychologist_id = '...';
  SET LOCAL acolhe.user_role = 'psychologist';
  ```
- `documentary_record` (Registro Documental) é **só do psicólogo autor** — tabela
  separada de propósito, nunca aparece em queries do paciente.
- `audit_log` e `consent` são **append-only**: revogar UPDATE/DELETE da role da app
  (ver `migrations/0002_rls.sql`).
- Campos com `*_encrypted` (CPF, conteúdo documental) recebem criptografia a nível
  de coluna via KMS — ainda não implementado nesta base.
