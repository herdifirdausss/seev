#!/bin/sh
# Runs once, at first container boot, as the postgres image's bootstrap
# superuser (POSTGRES_USER — the schema-owner identity, see docker-compose.yml
# and docs/plan/16 Task T3). Creates the LOGIN role the application actually
# connects as (APP_DB_USER/APP_DB_PASSWORD) — group-role membership in
# app_service is granted separately, AFTER migration 000009 creates that
# role (see `make grant-app-role` in the Makefile). A role with no group
# membership yet simply can't do anything under RLS — safe default.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    DO \$\$
    BEGIN
        IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${APP_DB_USER}') THEN
            EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', '${APP_DB_USER}', '${APP_DB_PASSWORD}');
        END IF;
    END
    \$\$;
EOSQL
