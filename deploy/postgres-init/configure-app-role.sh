#!/bin/sh
set -eu

: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_APP_PASSWORD:?POSTGRES_APP_PASSWORD is required}"
database_host="${PGHOST:-postgres}"

psql \
	--host "$database_host" \
	--username "$POSTGRES_USER" \
	--dbname "$POSTGRES_DB" \
	--set ON_ERROR_STOP=1 \
	--set database="$POSTGRES_DB" \
	--set owner="$POSTGRES_USER" \
	--set app_password="$POSTGRES_APP_PASSWORD" <<'SQL'
SELECT format(
  'CREATE ROLE acolhe_app LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS',
  :'app_password'
)
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'acolhe_app')
\gexec

SELECT format('ALTER ROLE acolhe_app PASSWORD %L', :'app_password')
\gexec

GRANT CONNECT ON DATABASE :"database" TO acolhe_app;
GRANT USAGE ON SCHEMA public TO acolhe_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO acolhe_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO acolhe_app;

SELECT format(
  'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO acolhe_app',
  :'owner'
)
\gexec
SELECT format(
  'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO acolhe_app',
  :'owner'
)
\gexec
SQL
