#!/bin/sh
# Bootstrap every service schema on a fresh Compose volume. The application
# containers can therefore start directly with `docker compose --profile app
# up` while golang-migrate remains the canonical tool for later upgrades.
set -eu

for service in ledger auth payin payout fraud gateway; do
    database="seev_${service}"
    latest=0

    for migration in "/migrations/${service}"/*.up.sql; do
        [ -f "$migration" ] || continue
        psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$database" -f "$migration"
        prefix="$(basename "$migration" | cut -d_ -f1 | sed 's/^0*//')"
        latest="${prefix:-0}"
    done

    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$database" <<-EOSQL
        CREATE TABLE IF NOT EXISTS schema_migrations_${service} (
            version BIGINT NOT NULL PRIMARY KEY,
            dirty BOOLEAN NOT NULL
        );
        DELETE FROM schema_migrations_${service};
        INSERT INTO schema_migrations_${service} (version, dirty) VALUES (${latest}, false);
EOSQL
done

# Role membership is cluster-wide. The table privileges and RLS policies are
# still scoped independently inside each service database.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
    GRANT app_service TO ledger_app, auth_app, payin_app, payout_app, fraud_app, gateway_app;
EOSQL
