#!/usr/bin/env sh
set -eu

: "${ANALYTICS_CONFIRM_SLOT_DROP:?set ANALYTICS_CONFIRM_SLOT_DROP=source-risk to drop C2 slots/publications}"
[ "$ANALYTICS_CONFIRM_SLOT_DROP" = source-risk ] || { echo "drop-replication: confirmation mismatch" >&2; exit 2; }
: "${POSTGRES_HOST:=127.0.0.1}"
: "${POSTGRES_PORT:=5433}"
: "${POSTGRES_MIGRATE_USER:=seev}"
: "${POSTGRES_MIGRATE_PASSWORD:=seev}"

PGPASSWORD="$POSTGRES_MIGRATE_PASSWORD" psql \
  --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
  --username "$POSTGRES_MIGRATE_USER" --dbname seev_ledger \
  --set ON_ERROR_STOP=1 <<'SQL'
DROP PUBLICATION IF EXISTS seev_analytics_ledger_pub;
SELECT pg_drop_replication_slot(slot_name)
FROM pg_replication_slots
WHERE slot_name = 'seev_analytics_ledger_slot';
DROP ROLE IF EXISTS seev_analytics_ledger;
SQL

PGPASSWORD="$POSTGRES_MIGRATE_PASSWORD" psql \
  --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
  --username "$POSTGRES_MIGRATE_USER" --dbname seev_payin \
  --set ON_ERROR_STOP=1 <<'SQL'
DROP PUBLICATION IF EXISTS seev_analytics_payin_pub;
SELECT pg_drop_replication_slot(slot_name)
FROM pg_replication_slots
WHERE slot_name = 'seev_analytics_payin_slot';
DROP ROLE IF EXISTS seev_analytics_payin;
SQL

PGPASSWORD="$POSTGRES_MIGRATE_PASSWORD" psql \
  --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
  --username "$POSTGRES_MIGRATE_USER" --dbname seev_payout \
  --set ON_ERROR_STOP=1 <<'SQL'
DROP PUBLICATION IF EXISTS seev_analytics_payout_pub;
SELECT pg_drop_replication_slot(slot_name)
FROM pg_replication_slots
WHERE slot_name = 'seev_analytics_payout_slot';
DROP ROLE IF EXISTS seev_analytics_payout;
SQL

echo "analytics replication objects dropped; future CDC requires a fresh snapshot"
