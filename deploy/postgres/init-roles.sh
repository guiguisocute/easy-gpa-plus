#!/bin/sh
set -eu

# PostgreSQL runs this only when the data directory is first created. The owner
# runs migrations; the HTTP and ops roles receive deliberately disjoint grants
# from migration 000002.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  --set=db_name="$POSTGRES_DB" \
  --set=app_password="${POSTGRES_APP_PASSWORD:-easygpa-app-dev}" \
  --set=ops_password="${POSTGRES_OPS_PASSWORD:-easygpa-ops-dev}" <<'EOSQL'
SELECT format('CREATE ROLE easygpa_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD %L', :'app_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') \gexec

SELECT format('CREATE ROLE easygpa_ops LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS PASSWORD %L', :'ops_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_ops') \gexec

GRANT CONNECT ON DATABASE :"db_name" TO easygpa_app, easygpa_ops;
EOSQL
