#!/usr/bin/env bash
# End-to-end durable schedule failure-policy journey (C5 Part B):
# once-schedule happy path → occurrence succeeded → ledger balanced;
# daily-skip policy → 3 overdue occurrences skipped_missed + 1 executed today.
#
# Tests docs/roadmap/active/61-c5-advanced-financial-products-period-close.md §3
# against a real assembled Ledger process using the durable schedule runner
# (FinancialProductsEnabled=true), so the PlanSchedule → claim → ExecuteOccurrence
# → txLookup path is exercised against a real Postgres schema.
#
# Requires: Docker running, go toolchain available.
# Does NOT require the app stack to already be running.
#
# Usage:
#   ./scripts/schedule-policy-e2e.sh
#
# The feature flag is set ONLY for this script's own service startup, not for
# the repo-wide default. Shared bootstrap lives in scripts/lib.sh.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="schedule-policy-e2e"
LIB_WORK_DIR_PREFIX="schedule-policy-e2e"
export C5_FINANCIAL_PRODUCTS_ENABLED=true
source "$ROOT_DIR/scripts/lib.sh"
trap cleanup EXIT

RUN_ID="${RUN_ID:-$(date +%s)}"

ensure_deps_up
build_server
start_services

ADMIN_TOKEN="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"

ledger_internal() {
	local method=$1 path=$2 body=${3:-}
	local token=${4:-$ADMIN_TOKEN}
	if [ -n "$body" ]; then
		curl_internal -sS -X "$method" \
			"https://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger$path" \
			-H "Authorization: Bearer $token" -H "Content-Type: application/json" -d "$body"
	else
		curl_internal -sS -X "$method" \
			"https://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger$path" \
			-H "Authorization: Bearer $token"
	fi
}

# wait_for_occurrence_status polls a scheduled_occurrences row via psql until
# its status matches want or tries (default 20, 1s apart) are exhausted.
wait_for_occurrence_status() {
	local schedule_id=$1 want=$2 tries=${3:-20}
	local status=""
	while [ "$tries" -gt 0 ]; do
		status="$(psql_exec "$LEDGER_DB_NAME" \
			-c "SELECT status FROM scheduled_occurrences WHERE schedule_id='$schedule_id' \
			    AND status NOT IN ('planned','due','ready','retry_wait') LIMIT 1;" 2>/dev/null || true)"
		status="$(printf '%s' "$status" | tr -d '[:space:]')"
		[ "$status" = "$want" ] && return 0
		sleep 1
		tries=$((tries - 1))
	done
	fail "schedule $schedule_id did not reach occurrence status '$want' in time (last: '$status')"
	return 1
}

TODAY="$(date +%Y-%m-%d)"
THREE_DAYS_AGO="$(date -v-3d +%Y-%m-%d 2>/dev/null || date -d '3 days ago' +%Y-%m-%d)"

# ─── 1. Provision users ───────────────────────────────────────────────────────

log "=== 1. Provision users A and B ==="

USER_A="$(uuidgen | tr '[:upper:]' '[:lower:]')"
USER_B="$(uuidgen | tr '[:upper:]' '[:lower:]')"
provision_user "$USER_A" 500000 >/dev/null
provision_user "$USER_B" 0 >/dev/null
ok "user A provisioned and funded 500,000 IDR (user=$USER_A)"
ok "user B provisioned (user=$USER_B)"

TOKEN_A="$(gen_token "$USER_A")"
TOKEN_B="$(gen_token "$USER_B")"

BALANCE_A_BEFORE="$(account_balance "$(cash_account_id "$USER_A")")"
BALANCE_B_BEFORE="$(account_balance "$(cash_account_id "$USER_B")")"

# ─── 2. Once-schedule: happy path ────────────────────────────────────────────

log "=== 2. Once-schedule: A→B transfer of 5000, run today ==="

schedule_json="$(ledger_internal POST /schedules \
	"{\"type\":\"transfer_p2p\",\"amount\":\"5000\",\
\"target_user_id\":\"$USER_B\",\"schedule_kind\":\"once\",\
\"run_at_date\":\"$TODAY\"}" "$TOKEN_A")"
SCHEDULE_A="$(echo "$schedule_json" | json_field id)"
[ -n "$SCHEDULE_A" ] && ok "once-schedule created (id=$SCHEDULE_A)" \
	|| fail "once-schedule creation failed: $schedule_json"

# Trigger the durable schedule runner for today.
run_json="$(ledger_internal POST "/admin/schedules/run?date=$TODAY" '{}' "$ADMIN_TOKEN")"
ok "schedule runner triggered for $TODAY; response: $run_json"

# Poll for the occurrence to reach a terminal state.
wait_for_occurrence_status "$SCHEDULE_A" "succeeded" 20 \
	&& ok "once-schedule occurrence status = succeeded" \
	|| fail "once-schedule did not reach 'succeeded'"

BALANCE_A_AFTER="$(account_balance "$(cash_account_id "$USER_A")")"
BALANCE_B_AFTER="$(account_balance "$(cash_account_id "$USER_B")")"

[ "$((BALANCE_A_BEFORE - BALANCE_A_AFTER))" = "5000" ] \
	&& ok "user A debited exactly 5000 (before=$BALANCE_A_BEFORE after=$BALANCE_A_AFTER)" \
	|| fail "user A balance delta unexpected: before=$BALANCE_A_BEFORE after=$BALANCE_A_AFTER"
[ "$((BALANCE_B_AFTER - BALANCE_B_BEFORE))" = "5000" ] \
	&& ok "user B credited exactly 5000 (before=$BALANCE_B_BEFORE after=$BALANCE_B_AFTER)" \
	|| fail "user B balance delta unexpected: before=$BALANCE_B_BEFORE after=$BALANCE_B_AFTER"

assert_ledger_balanced

# ─── 3. Daily-skip policy: 3 missed → skipped_missed + 1 today executed ──────

log "=== 3. Daily-skip policy: schedule starting 3 days ago, run today ==="

# 'daily' schedule_kind → the SQL sets missed_run_policy='skip' automatically
# (services/ledger/internal/repository/scheduled_transaction_repository.go Create).
# A once-schedule wouldn't need the policy; this proves daily-skipping behavior.
schedule_json2="$(ledger_internal POST /schedules \
	"{\"type\":\"transfer_p2p\",\"amount\":\"1000\",\
\"target_user_id\":\"$USER_B\",\"schedule_kind\":\"daily\",\
\"run_at_date\":\"$THREE_DAYS_AGO\"}" "$TOKEN_A")"
SCHEDULE_B="$(echo "$schedule_json2" | json_field id)"
[ -n "$SCHEDULE_B" ] && ok "daily-schedule created (id=$SCHEDULE_B run_at=$THREE_DAYS_AGO)" \
	|| fail "daily-schedule creation failed: $schedule_json2"

# Running for today plans 3 overdue occurrences as skipped_missed + executes today.
run_json2="$(ledger_internal POST "/admin/schedules/run?date=$TODAY" '{}' "$ADMIN_TOKEN")"
ok "schedule runner triggered for $TODAY (daily skip); response: $run_json2"

# Poll for today's occurrence to succeed.
wait_for_occurrence_status "$SCHEDULE_B" "succeeded" 20 \
	&& ok "today's daily-schedule occurrence status = succeeded" \
	|| fail "daily-schedule today-occurrence did not reach 'succeeded'"

# Three past occurrences must be skipped_missed.
skipped_count="$(psql_exec "$LEDGER_DB_NAME" \
	-c "SELECT count(*) FROM scheduled_occurrences \
	    WHERE schedule_id='$SCHEDULE_B' AND status='skipped_missed';" \
	| tr -d '[:space:]')"
[ "$skipped_count" = "3" ] && ok "3 overdue occurrences are skipped_missed (skip policy honored)" \
	|| fail "expected 3 skipped_missed rows, got $skipped_count"

# Total occurrence rows: 3 skipped + 1 succeeded.
total_count="$(psql_exec "$LEDGER_DB_NAME" \
	-c "SELECT count(*) FROM scheduled_occurrences WHERE schedule_id='$SCHEDULE_B';" \
	| tr -d '[:space:]')"
[ "$total_count" = "4" ] && ok "4 total occurrence rows for daily-skip schedule" \
	|| fail "expected 4 occurrence rows, got $total_count"

BALANCE_A_SKIP="$(account_balance "$(cash_account_id "$USER_A")")"
BALANCE_B_SKIP="$(account_balance "$(cash_account_id "$USER_B")")"
[ "$((BALANCE_A_AFTER - BALANCE_A_SKIP))" = "1000" ] \
	&& ok "user A debited 1000 for daily occurrence (only the one that ran)" \
	|| fail "user A daily-skip delta unexpected: before=$BALANCE_A_AFTER after=$BALANCE_A_SKIP"

assert_ledger_balanced
assert_no_inconsistent_projections
assert_no_stuck_pending_transactions

if [ "${FAILED:-0}" -ne 0 ]; then
	exit 1
fi
log "schedule-policy-e2e completed"
