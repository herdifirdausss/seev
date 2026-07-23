#!/usr/bin/env bash
# Point-in-time rebuild of account_balances from ledger_entries (docs/roadmap/archive/17
# Task T2, decision S9) — empirical proof that the balance projection can be
# rebuilt 100% from the append-only ledger, for use after a restore or
# whenever a projection inconsistency is suspected.
#
# Refuses to run while the app is live (rebuilding under concurrent posting
# traffic races the posting engine's own balance writes) — this is a
# maintenance-window procedure, not a hot-path repair tool.
#
# Uses scripts/sql/rebuild_projection.sql — the SAME file the Go integration
# test (TestSchemaContract_RebuildProjection) runs, so there is exactly one
# copy of the rebuild SQL to keep correct.
#
# Connects as the app's normal restricted role (POSTGRES_USER, app_service
# member) — no elevated privilege needed: app_service already has
# SELECT+INSERT+UPDATE on account_balances (docs/roadmap/archive/16 Task T3) and the
# rebuild only SELECTs ledger_entries + UPDATEs account_balances.
#
# Usage:
#   POSTGRES_HOST=... POSTGRES_PORT=... POSTGRES_USER=... POSTGRES_PASSWORD=... POSTGRES_DB=... \
#     ./scripts/rebuild-projection.sh
#
# With no POSTGRES_* env set, defaults to the local docker-compose dev stack
# (container seev-postgres-1, role seev_app — docs/roadmap/archive/16 Task T3).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

APP_HEALTH_URL="${APP_HEALTH_URL:-http://localhost:${APP_PORT:-8080}/health}"
REBUILD_SQL="scripts/sql/rebuild_projection.sql"

POSTGRES_HOST="${POSTGRES_HOST:-}"
# 5433 matches docker-compose.yml's default host port for postgres (see its
# comment) — dodges a native Postgres commonly already on 5432.
POSTGRES_PORT="${POSTGRES_PORT:-5433}"
POSTGRES_USER="${POSTGRES_USER:-seev_app}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-seev_app}"
POSTGRES_DB="${POSTGRES_DB:-seev}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-seev-postgres-1}"

log()  { printf '\033[1;34m[rebuild]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[ pass]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[ FAIL]\033[0m %s\n' "$*"; }

run_psql() {
	if [ -n "$POSTGRES_HOST" ]; then
		PGPASSWORD="$POSTGRES_PASSWORD" psql -h "$POSTGRES_HOST" -p "$POSTGRES_PORT" \
			-U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 "$@"
	else
		docker exec -i -e PGPASSWORD="$POSTGRES_PASSWORD" "$POSTGRES_CONTAINER" \
			psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 "$@"
	fi
}

# run_psql_file runs a SQL file from the HOST filesystem. `-f <path>` only
# works for a direct connection (the path is resolved locally by psql); for
# the docker-exec mode the path means nothing INSIDE the container, so the
# file must be piped over stdin instead — `docker exec -i` forwards this
# process's stdin into the container's psql.
run_psql_file() {
	local file=$1
	if [ -n "$POSTGRES_HOST" ]; then
		PGPASSWORD="$POSTGRES_PASSWORD" psql -h "$POSTGRES_HOST" -p "$POSTGRES_PORT" \
			-U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -f "$file"
	else
		docker exec -i -e PGPASSWORD="$POSTGRES_PASSWORD" "$POSTGRES_CONTAINER" \
			psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 <"$file"
	fi
}

# ─── Step 1: refuse to run while the app is live ───────────────────────────

if curl -sf -o /dev/null --max-time 2 "$APP_HEALTH_URL" 2>/dev/null; then
	fail "the app is responding at $APP_HEALTH_URL — this is a maintenance-window procedure, stop the app first"
	exit 1
fi
log "app is not responding at $APP_HEALTH_URL — proceeding"

# ─── Step 2: snapshot pre-rebuild state for the diff report ────────────────

log "capturing pre-rebuild balances..."
PRE_COUNT=$(run_psql -t -A -c "SELECT count(*) FROM account_balances")
log "account_balances has $PRE_COUNT rows"

# ─── Step 3: run the rebuild (the SQL file both this script and the Go test use) ───

log "running $REBUILD_SQL..."
run_psql_file "$REBUILD_SQL"

# ─── Step 4: verify every account is now consistent ────────────────────────

log "verifying every account is consistent with ledger_entries..."
INCONSISTENT=$(run_psql -t -A -c "
	SELECT count(*) FROM account_balances ab
	WHERE ab.balance IS DISTINCT FROM (
	    SELECT COALESCE(SUM(amount) FILTER (WHERE direction='credit'), 0) -
	           COALESCE(SUM(amount) FILTER (WHERE direction='debit'),  0)
	    FROM ledger_entries WHERE account_id = ab.account_id
	)")

if [ "$INCONSISTENT" != "0" ]; then
	fail "rebuild left $INCONSISTENT account(s) inconsistent — investigate before restarting the app"
	exit 1
fi
ok "all $PRE_COUNT accounts consistent with ledger_entries after rebuild"

UNBALANCED=$(run_psql -t -A -c "SELECT count(*) FROM fn_verify_ledger_balance('-infinity', 'infinity')")
if [ "$UNBALANCED" != "0" ]; then
	fail "fn_verify_ledger_balance() found $UNBALANCED unbalanced transaction(s) — this is a ledger integrity issue, not a projection issue; see docs/operations/runbooks/ledger-integrity-alert.md"
	exit 1
fi
ok "fn_verify_ledger_balance() found 0 unbalanced transactions"

log "rebuild complete — safe to restart the app"
