#!/usr/bin/env bash
# End-to-end interest capitalization journey (C5 Part A):
# create product → approve rate → enroll → RunDaily → ClosePeriod →
# period immutable (second close rejected, DB trigger blocks raw UPDATE).
#
# Tests docs/roadmap/active/61-c5-advanced-financial-products-period-close.md §3
# against a real assembled Ledger process with all migrations applied, so schema
# correctness (including the fn_prevent_c5_closed_period_mutation trigger) is
# exercised alongside the Go service layer.
#
# Requires: Docker running, go toolchain, openssl available.
# Does NOT require the app stack to already be running — this script builds and
# manages its own server processes and docker-compose dependencies.
#
# Usage:
#   ./scripts/interest-period-e2e.sh
#
# The feature flag is set ONLY for this script's own service startup, not for the
# repo-wide default. Shared bootstrap lives in scripts/lib.sh.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="interest-period-e2e"
LIB_WORK_DIR_PREFIX="interest-period-e2e"
export C5_FINANCIAL_PRODUCTS_ENABLED=true
source "$ROOT_DIR/scripts/lib.sh"
trap cleanup EXIT

RUN_ID="${RUN_ID:-$(date +%s)}"

ensure_deps_up
build_server
start_services

# System account IDs seeded by migration 000040 (C5 — monthly capitalization).
INTEREST_EXPENSE_IDR="00000000-0000-0000-0000-000000000029"
INTEREST_PAYABLE_IDR="00000000-0000-0000-0000-000000000031"

MAKER_TOKEN="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin_maker)"
CHECKER_TOKEN="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin_checker)"
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

# ─── 1. Savings product: create → activate → rate → submit → approve ──────────

log "=== 1. Create and activate savings product (IDR, 500 bps) ==="

product_json="$(ledger_internal POST /admin/savings/products \
	"{\"product_code\":\"E2E-BASIC-$RUN_ID\",\"name\":\"E2E Interest Basic\",\"currency\":\"IDR\",\
\"interest_expense_account_id\":\"$INTEREST_EXPENSE_IDR\",\
\"interest_payable_account_id\":\"$INTEREST_PAYABLE_IDR\",\
\"eligible_account_types\":[\"cash\"]}" "$MAKER_TOKEN")"
PRODUCT_ID="$(echo "$product_json" | json_field id)"
[ -n "$PRODUCT_ID" ] && ok "savings product created ($PRODUCT_ID)" \
	|| fail "savings product creation failed: $product_json"

status_json="$(ledger_internal POST "/admin/savings/products/$PRODUCT_ID/status" \
	'{"status":"active"}' "$CHECKER_TOKEN")"
echo "$status_json" | grep -q '"status":"active"' && ok "product activated" \
	|| fail "product activation failed: $status_json"

rate_json="$(ledger_internal POST "/admin/savings/products/$PRODUCT_ID/rates" \
	'{"annual_rate_bps":500,"effective_from":"2025-01-01"}' "$MAKER_TOKEN")"
RATE_ID="$(echo "$rate_json" | json_field id)"
[ -n "$RATE_ID" ] && ok "rate version created ($RATE_ID)" \
	|| fail "rate creation failed: $rate_json"

ledger_internal POST "/admin/savings/rates/$RATE_ID/submit" '{}' "$MAKER_TOKEN" >/dev/null
ok "rate submitted by maker"
ledger_internal POST "/admin/savings/rates/$RATE_ID/approve" '{}' "$CHECKER_TOKEN" >/dev/null
ok "rate approved by checker"

# ─── 2. User + enrollment ─────────────────────────────────────────────────────

log "=== 2. Provision user, fund, enroll in savings ==="

USER_ID="$(uuidgen | tr '[:upper:]' '[:lower:]')"
CASH_ACCOUNT_ID="$(provision_user "$USER_ID" 1000000)"
[ -n "$CASH_ACCOUNT_ID" ] && ok "user provisioned and funded (user=$USER_ID account=$CASH_ACCOUNT_ID)" \
	|| fail "user provisioning failed"

# EffectiveFrom must match the accrual date so expected_item_count = 1 for the
# January 2026 period (2026-02-01 boundary - 2026-01-31 effective_from = 1 day).
enrollment_json="$(ledger_internal POST /admin/savings/enrollments \
	"{\"product_id\":\"$PRODUCT_ID\",\"account_id\":\"$CASH_ACCOUNT_ID\",\
\"user_id\":\"$USER_ID\",\"effective_from\":\"2026-01-31\"}" "$MAKER_TOKEN")"
ENROLLMENT_ID="$(echo "$enrollment_json" | json_field id)"
[ -n "$ENROLLMENT_ID" ] && ok "enrollment created ($ENROLLMENT_ID)" \
	|| fail "enrollment creation failed: $enrollment_json"

# Seed a balance snapshot for the synthetic accrual date. RunDaily reads the
# snapshot, not live entries, so this is required for any past date.
psql_exec "$LEDGER_DB_NAME" -c "
	INSERT INTO account_balance_snapshots
		(account_id, as_of_date, closing_balance, entry_count)
	VALUES ('$CASH_ACCOUNT_ID', '2026-01-31', 1000000, 1)
	ON CONFLICT DO NOTHING;" >/dev/null
ok "balance snapshot seeded for 2026-01-31 (1,000,000 IDR)"

# ─── 3. RunDaily ──────────────────────────────────────────────────────────────

log "=== 3. RunInterestDaily for 2026-01-31 ==="

run_json="$(ledger_internal POST "/admin/savings/interest/run?date=2026-01-31" '{}' "$ADMIN_TOKEN")"
ok "RunDaily triggered; summary: $run_json"

# RunDaily is synchronous — the accrual should be present immediately. The poll
# loop is defensive against slow CI environments.
USER_TOKEN="$(gen_token "$USER_ID")"
accrual_status=""
tries=15
while [ "$tries" -gt 0 ]; do
	accruals_json="$(ledger_internal GET "/savings/enrollments/$ENROLLMENT_ID/accruals" "" "$USER_TOKEN")"
	accrual_status="$(echo "$accruals_json" | grep -o '"status":"[^"]*"' | head -1 | sed 's/.*"status":"//;s/".*//')"
	[ "$accrual_status" = "completed_posted" ] && break
	sleep 1
	tries=$((tries - 1))
done
[ "$accrual_status" = "completed_posted" ] \
	&& ok "accrual status is completed_posted (balance=1000000; rate=500bps; ~136 IDR recognized)" \
	|| fail "accrual status was '$accrual_status', expected completed_posted (response: $accruals_json)"

# ─── 4. Period lookup ─────────────────────────────────────────────────────────

periods_json="$(ledger_internal GET "/savings/enrollments/$ENROLLMENT_ID/periods" "" "$USER_TOKEN")"
PERIOD_ID="$(echo "$periods_json" | json_field id)"
[ -n "$PERIOD_ID" ] && ok "interest period found ($PERIOD_ID)" \
	|| fail "no period found: $periods_json"

# ─── 5. Preview and close ─────────────────────────────────────────────────────

log "=== 4. PreviewPeriodClose → must show 0 missing items ==="

preview_json="$(ledger_internal GET "/admin/savings/periods/$PERIOD_ID/preview" "" "$ADMIN_TOKEN")"
missing="$(echo "$preview_json" | grep -o '"missing_items":[0-9]*' | head -1 | cut -d: -f2)"
[ "$missing" = "0" ] && ok "preview: 0 missing items — ready to close" \
	|| fail "preview shows missing_items=$missing, period not ready: $preview_json"

log "=== 5. ClosePeriod ==="

close_json="$(ledger_internal POST "/admin/savings/periods/$PERIOD_ID/close" '{}' "$CHECKER_TOKEN")"
echo "$close_json" | grep -q '"closed":true' && ok "ClosePeriod succeeded — period closed" \
	|| fail "ClosePeriod failed: $close_json"

# ─── 6. Period immutability ───────────────────────────────────────────────────

log "=== 6. Verify period status + immutability guard ==="

periods_json2="$(ledger_internal GET "/savings/enrollments/$ENROLLMENT_ID/periods" "" "$USER_TOKEN")"
period_status="$(echo "$periods_json2" | grep -o '"status":"[^"]*"' | head -1 | sed 's/.*"status":"//;s/".*//')"
[ "$period_status" = "closed" ] && ok "period status is 'closed' after ClosePeriod" \
	|| fail "period status was '$period_status', expected closed"

# Second close attempt must be rejected (ErrClosedPeriodImmutable → 422).
second_close_code="$(curl_internal -sS -o /dev/null -w '%{http_code}' -X POST \
	"https://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/savings/periods/$PERIOD_ID/close" \
	-H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" -d '{}')"
[ "$second_close_code" != "200" ] \
	&& ok "second ClosePeriod rejected with code=$second_close_code — period is immutable" \
	|| fail "second ClosePeriod returned 200; ErrClosedPeriodImmutable was not enforced"

# DB-level immutability: fn_prevent_c5_closed_period_mutation trigger must
# reject a raw UPDATE on the closed period row.
trigger_result="$(psql_exec "$LEDGER_DB_NAME" \
	-c "UPDATE interest_periods SET status='open' WHERE id='$PERIOD_ID';" 2>&1 || true)"
echo "$trigger_result" | grep -qi "error\|prevent\|trigger\|immutable" \
	&& ok "fn_prevent_c5_closed_period_mutation trigger blocked raw UPDATE on closed period" \
	|| fail "DB trigger did not fire on raw UPDATE of closed period: $trigger_result"

# ─── 7. Ledger integrity ──────────────────────────────────────────────────────

log "=== 7. Ledger integrity assertions ==="

assert_ledger_balanced
assert_no_inconsistent_projections
assert_no_stuck_pending_transactions

if [ "${FAILED:-0}" -ne 0 ]; then
	exit 1
fi
log "interest-period-e2e completed"
