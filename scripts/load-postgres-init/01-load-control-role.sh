#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'load_observer') THEN
    CREATE ROLE load_observer LOGIN PASSWORD 'B0observer000000000000000000000000000000000000000000000000000000';
  END IF;
END
$$;
GRANT pg_monitor TO load_observer;
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
SQL
