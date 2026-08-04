#!/bin/sh
set -eu
for service in ledger auth payin payout fraud gateway vendor; do
  database="seev_load_${service}"
  role="${service}_app"
  migration_service="${service}"
  [ "$service" = vendor ] && migration_service=vendor-service
  latest=0
  for migration in "/services/${migration_service}/migrations"/*.up.sql; do
    [ -f "$migration" ] || continue
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$database" -f "$migration"
    prefix="$(basename "$migration" | cut -d_ -f1 | sed 's/^0*//')"
    latest="${prefix:-0}"
  done
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$database" <<-EOSQL
    CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
    CREATE TABLE IF NOT EXISTS schema_migrations_${service} (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL);
    DELETE FROM schema_migrations_${service};
    INSERT INTO schema_migrations_${service} (version, dirty) VALUES (${latest}, false);
    DO \$\$
    BEGIN
      IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_service') THEN
        EXECUTE 'GRANT app_service TO ${role}';
      END IF;
    END
    \$\$;
EOSQL
done
