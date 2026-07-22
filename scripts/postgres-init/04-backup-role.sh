#!/bin/sh
# Provision the least-privilege seev_backup role (docs/plan/50 K5) on the
# first boot of a fresh Postgres volume. This is idempotent by construction
# (CREATE ROLE IF NOT EXISTS / GRANT is itself idempotent) so it is also the
# body `make backup-role-bootstrap` runs against an EXISTING volume — this
# script is never modified to differ between the two call sites; the only
# difference is who invokes it and when. Runs as the schema-owner bootstrap
# identity (POSTGRES_USER) via /docker-entrypoint-initdb.d on first boot,
# same as 02-service-dbs.sh and 03-service-migrations.sh.
#
# seev_backup deliberately gets:
#   - LOGIN + REPLICATION (pgBackRest's physical backup/WAL-streaming
#     protocol requires a replication-capable role, not ordinary CONNECT);
#   - CONNECT on every one of the eight authoritative service databases;
#   - SELECT on each database's own schema_migrations_<service> table ONLY.
# It never gets access to a single domain table. Backup and status tooling
# must use this identity for routine operation, never the application roles
# or the schema-owner password (docs/plan/50 K5).
set -eu

BACKUP_PASSWORD_FILE="${BACKUP_PASSWORD_FILE:-/run/secrets/seev_backup_password}"
if [ ! -f "$BACKUP_PASSWORD_FILE" ]; then
    echo "04-backup-role.sh: $BACKUP_PASSWORD_FILE not found — run 'make backup-secret' first, then re-run this bootstrap. Skipping (backup role not created)." >&2
    exit 0
fi
BACKUP_PASSWORD="$(cat "$BACKUP_PASSWORD_FILE")"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
    DO \$\$
    BEGIN
        IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'seev_backup') THEN
            CREATE ROLE seev_backup LOGIN REPLICATION PASSWORD '${BACKUP_PASSWORD}';
        ELSE
            ALTER ROLE seev_backup LOGIN REPLICATION PASSWORD '${BACKUP_PASSWORD}';
        END IF;
    END
    \$\$;
    -- pg_read_all_settings (built-in Postgres predefined role, not a domain
    -- grant) is a genuine pgBackRest functional requirement — it checks
    -- server configuration (e.g. wal_level, data_directory) via pg_settings
    -- during every stanza-create/backup/check. This is Postgres's own
    -- configuration catalog, not business data, so it does not weaken K5's
    -- "no domain-table access" rule.
    GRANT pg_read_all_settings TO seev_backup;
    -- Postgres restricts EXECUTE on the backup-control functions by
    -- default even for REPLICATION-attribute roles — pgBackRest calls
    -- these directly to bracket its physical backup (never table data).
    GRANT EXECUTE ON FUNCTION pg_backup_start(text, boolean) TO seev_backup;
    GRANT EXECUTE ON FUNCTION pg_backup_stop(boolean) TO seev_backup;
    -- pgbackrest's `check` command round-trips a restore point through
    -- the WAL archive to prove archive_command is actually working end to
    -- end, not just configured.
    GRANT EXECUTE ON FUNCTION pg_create_restore_point(text) TO seev_backup;
    -- `check` also forces a WAL segment switch to prove the archive
    -- pipeline is current, not just reachable.
    GRANT EXECUTE ON FUNCTION pg_switch_wal() TO seev_backup;
EOSQL

for service in ledger auth payin payout fraud gateway adminbff assurance; do
    database="seev_${service}"
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
        -c "GRANT CONNECT ON DATABASE ${database} TO seev_backup;"

    # The migration table may not exist yet if this runs before
    # 03-service-migrations.sh on a fresh volume in some future reordering —
    # tolerate that ordering accident loudly rather than silently, since a
    # missing grant here is exactly the kind of thing K5 exists to prevent
    # from going unnoticed.
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$database" <<-EOSQL
        DO \$\$
        BEGIN
            IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'schema_migrations_${service}') THEN
                EXECUTE format('GRANT SELECT ON %I TO seev_backup', 'schema_migrations_${service}');
            ELSE
                RAISE WARNING 'schema_migrations_${service} does not exist yet in %; seev_backup grant skipped for this database until it does', current_database();
            END IF;
        END
        \$\$;
EOSQL
done

# pgBackRest's physical-backup/WAL-streaming protocol needs an explicit
# replication pg_hba.conf entry — PostgreSQL's "all" database keyword does
# NOT match replication connections (they are matched only by the literal
# "replication" keyword). Appended once, idempotently.
HBA_FILE="$(psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres -tAc "SHOW hba_file;")"
if ! grep -q "^host[[:space:]]\+replication[[:space:]]\+seev_backup" "$HBA_FILE" 2>/dev/null; then
    echo "host replication seev_backup 0.0.0.0/0 scram-sha-256" >> "$HBA_FILE"
    echo "host replication seev_backup ::0/0 scram-sha-256" >> "$HBA_FILE"
fi
