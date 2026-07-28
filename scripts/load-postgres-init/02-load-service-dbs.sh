#!/bin/sh
set -eu
for service in ledger auth payin payout fraud gateway vendor; do
  database="seev_load_${service}"
  role="${service}_app"
  if [ "$(psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$database'")" != "1" ]; then
    createdb --username "$POSTGRES_USER" "$database"
  fi
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
    DO \$\$
    BEGIN
      IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${role}') THEN
        EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', '${role}', '${role}');
      END IF;
    END
    \$\$;
EOSQL
done
