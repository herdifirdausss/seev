#!/usr/bin/env sh
set -eu

: "${POSTGRES_HOST:=127.0.0.1}"
: "${POSTGRES_PORT:=5433}"
: "${POSTGRES_MIGRATE_USER:=seev}"
: "${POSTGRES_MIGRATE_PASSWORD:=seev}"
: "${ANALYTICS_LEDGER_PASSWORD:?ANALYTICS_LEDGER_PASSWORD is required}"
: "${ANALYTICS_PAYIN_PASSWORD:?ANALYTICS_PAYIN_PASSWORD is required}"
: "${ANALYTICS_PAYOUT_PASSWORD:?ANALYTICS_PAYOUT_PASSWORD is required}"

run_psql() {
  database=$1
  role=$2
  password=$3
  shift 3
  PGPASSWORD="$POSTGRES_MIGRATE_PASSWORD" psql \
    --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
    --username "$POSTGRES_MIGRATE_USER" --dbname "$database" \
    --set ON_ERROR_STOP=1 --set role_name="$role" --set role_password="$password" "$@"
}

run_psql seev_ledger seev_analytics_ledger "$ANALYTICS_LEDGER_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN REPLICATION PASSWORD %L', :'role_name', :'role_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role_name')\gexec
ALTER ROLE seev_analytics_ledger WITH LOGIN REPLICATION PASSWORD :'role_password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
GRANT CONNECT ON DATABASE seev_ledger TO seev_analytics_ledger;
GRANT USAGE ON SCHEMA public TO seev_analytics_ledger;
REVOKE CREATE ON SCHEMA public FROM seev_analytics_ledger;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM seev_analytics_ledger;
GRANT SELECT ON TABLE accounts, account_balances, ledger_transactions, ledger_entries, fee_quotes TO seev_analytics_ledger;
SELECT format('CREATE PUBLICATION %I FOR TABLE accounts, account_balances, ledger_transactions, ledger_entries, fee_quotes', 'seev_analytics_ledger_pub')
WHERE NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'seev_analytics_ledger_pub')\gexec
ALTER PUBLICATION seev_analytics_ledger_pub SET TABLE accounts, account_balances, ledger_transactions, ledger_entries, fee_quotes;
SQL

run_psql seev_payin seev_analytics_payin "$ANALYTICS_PAYIN_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN REPLICATION PASSWORD %L', :'role_name', :'role_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role_name')\gexec
ALTER ROLE seev_analytics_payin WITH LOGIN REPLICATION PASSWORD :'role_password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
GRANT CONNECT ON DATABASE seev_payin TO seev_analytics_payin;
GRANT USAGE ON SCHEMA public TO seev_analytics_payin;
REVOKE CREATE ON SCHEMA public FROM seev_analytics_payin;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM seev_analytics_payin;
GRANT SELECT ON TABLE payin_topup_intents, payin_webhook_events TO seev_analytics_payin;
SELECT format('CREATE PUBLICATION %I FOR TABLE payin_topup_intents, payin_webhook_events', 'seev_analytics_payin_pub')
WHERE NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'seev_analytics_payin_pub')\gexec
ALTER PUBLICATION seev_analytics_payin_pub SET TABLE payin_topup_intents, payin_webhook_events;
SQL

run_psql seev_payout seev_analytics_payout "$ANALYTICS_PAYOUT_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN REPLICATION PASSWORD %L', :'role_name', :'role_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role_name')\gexec
ALTER ROLE seev_analytics_payout WITH LOGIN REPLICATION PASSWORD :'role_password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
GRANT CONNECT ON DATABASE seev_payout TO seev_analytics_payout;
GRANT USAGE ON SCHEMA public TO seev_analytics_payout;
REVOKE CREATE ON SCHEMA public FROM seev_analytics_payout;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM seev_analytics_payout;
GRANT SELECT ON TABLE payout_requests, payout_vendor_calls TO seev_analytics_payout;
SELECT format('CREATE PUBLICATION %I FOR TABLE payout_requests, payout_vendor_calls', 'seev_analytics_payout_pub')
WHERE NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'seev_analytics_payout_pub')\gexec
ALTER PUBLICATION seev_analytics_payout_pub SET TABLE payout_requests, payout_vendor_calls;
SQL

echo "analytics replication roles and explicit publications applied"
