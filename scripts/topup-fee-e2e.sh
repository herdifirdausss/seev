#!/usr/bin/env bash
# End-to-end topup fee journey (C5 Part C):
# money_in fee rule → fee quote → topup money_in atomically consumes quote →
# idempotent replay does NOT double-charge → consuming an already-used quote
# is rejected (QUOTE_EXPIRED).
#
# Tests docs/roadmap/active/61-c5-advanced-financial-products-period-close.md §3
# using Ledger's internal API directly (the same path Payin takes after the
# vendor webhook arrives), so the C5 fee-consumption atomicity guarantee is
# exercised without needing the full Gateway→Payin→mockvendor webhook chain.
#
# Requires: Docker running, go toolchain available.
# Does NOT require the app stack to already be running.
#
# Usage:
#   ./scripts/topup-fee-e2e.sh

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="topup-fee-e2e"
LIB_WORK_DIR_PREFIX="topup-fee-e2e"
source "$ROOT_DIR/scripts/lib.sh"
trap cleanup EXIT

RUN_ID="${RUN_ID:-$(date +%s)}"

ensure_deps_up
build_server
start_services

MAKER_TOKEN="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin_maker)"
CHECKER_TOKEN="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin_checker)"

# ─── 1. Money-in fee rule ─────────────────────────────────────────────────────

log "=== 1. Create and approve money_in fee rule (500 IDR flat, bca gateway) ==="

FEE_URL="https://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/ledger/fee-rules"
FEE_RULE_ID=""

fee_rule_apply() {
	local existing_rule_id=$1 payload=$2 label=$3
	local draft_response draft_code draft_body version_id rule_id submit_code approve_code
	FEE_RULE_ID=""

	if [ -n "$existing_rule_id" ]; then
		draft_response="$(curl_internal -s -w '\n%{http_code}' -X PUT "$FEE_URL/$existing_rule_id" \
			-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" -d "$payload")"
	else
		draft_response="$(curl_internal -s -w '\n%{http_code}' -X POST "$FEE_URL" \
			-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" -d "$payload")"
	fi
	draft_code="${draft_response##*$'\n'}"
	draft_body="${draft_response%$'\n'*}"
	if [ "$draft_code" != "202" ]; then
		fail "$label draft got HTTP $draft_code: $draft_body"; return 0
	fi
	version_id="$(printf '%s' "$draft_body" | json_field id)"
	rule_id="$(printf '%s' "$draft_body" | json_field rule_id)"
	if [ -z "$version_id" ] || [ -z "$rule_id" ]; then
		fail "$label draft did not return ids: $draft_body"; return 0
	fi
	submit_code="$(curl_internal -s -o /dev/null -w '%{http_code}' -X POST "$FEE_URL/$version_id/submit" \
		-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" -d '{}')"
	[[ "$submit_code" == 2* ]] || { fail "$label submit got HTTP $submit_code"; return 0; }
	approve_code="$(curl_internal -s -o /dev/null -w '%{http_code}' -X POST "$FEE_URL/$version_id/approve" \
		-H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" \
		-d '{"reason":"topup-fee-e2e"}')"
	[[ "$approve_code" == 2* ]] || { fail "$label approve got HTTP $approve_code"; return 0; }
	FEE_RULE_ID="$rule_id"
	ok "$label applied through maker-checker (rule=$rule_id)"
}

fee_rule_apply "" \
	'{"tx_type":"money_in","currency":"IDR","gateway":"bca","flat_minor_units":500,"fee_gateway":"platform"}' \
	"money_in/bca fee rule (500 IDR flat)"
[ -n "$FEE_RULE_ID" ] || { echo "FAIL: fee rule not created" >&2; exit 1; }

# ─── 2. Provision user ────────────────────────────────────────────────────────

log "=== 2. Provision user ==="

USER_ID="$(uuidgen | tr '[:upper:]' '[:lower:]')"
provision_user "$USER_ID" 0 >/dev/null
ok "user provisioned (user=$USER_ID)"
USER_TOKEN="$(gen_token "$USER_ID")"

# ─── 3. Fee quote for money_in ────────────────────────────────────────────────

log "=== 3. Create fee quote for money_in (amount=100000, gateway=bca) ==="

# Fee quotes are only on the PUBLIC router (LEDGER_APP_PORT).
quote_resp="$(curl_internal -sS -X POST \
	"https://localhost:$LEDGER_APP_PORT/api/v1/ledger/fees/quote" \
	-H "Authorization: Bearer $USER_TOKEN" -H "Content-Type: application/json" \
	-d '{"transaction_type":"money_in","amount":"100000","gateway":"bca"}')"
QUOTE_ID="$(echo "$quote_resp" | json_field quote_id)"
quote_fee="$(echo "$quote_resp" | json_field fee_amount)"
[ -n "$QUOTE_ID" ] && [ "$quote_fee" = "500" ] \
	&& ok "fee quote created (id=$QUOTE_ID fee=500 IDR)" \
	|| fail "fee quote creation failed (expected fee=500): $quote_resp"

# ─── 4. money_in atomically consumes the quote ────────────────────────────────

log "=== 4. Post money_in with fee_quote_id via Ledger internal API ==="

fee_before="$(psql_exec "$LEDGER_DB_NAME" -c "
	SELECT COALESCE(b.balance,0) FROM accounts a
	JOIN account_balances b ON b.account_id=a.id
	WHERE a.owner_type='system' AND a.type='fee'
	  AND a.system_qualifier='platform' AND a.currency='IDR';" | tr -d '[:space:]')"

topup_code="$(curl_internal -sS -o "$WORK_DIR/topup1.json" -w '%{http_code}' \
	-X POST "https://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
	-H "Authorization: Bearer $USER_TOKEN" -H "Content-Type: application/json" \
	-d "{\"idempotency_key\":\"topup-fee-e2e-1-$RUN_ID\",\"type\":\"money_in\",\
\"amount\":\"100000\",\"quote_id\":\"$QUOTE_ID\",\"metadata\":{\"gateway\":\"bca\"}}")"
topup_body="$(cat "$WORK_DIR/topup1.json")"
[ "${topup_code:0:1}" = "2" ] && ok "money_in with fee quote accepted (code=$topup_code)" \
	|| fail "money_in with fee quote got $topup_code: $topup_body"

CASH_ID="$(cash_account_id "$USER_ID")"
user_balance="$(account_balance "$CASH_ID" | tr -d '[:space:]')"
[ "$user_balance" = "100000" ] \
	&& ok "user wallet credited full 100000 (fee is a separate ledger entry)" \
	|| fail "expected user balance=100000, got $user_balance"

fee_after="$(psql_exec "$LEDGER_DB_NAME" -c "
	SELECT COALESCE(b.balance,0) FROM accounts a
	JOIN account_balances b ON b.account_id=a.id
	WHERE a.owner_type='system' AND a.type='fee'
	  AND a.system_qualifier='platform' AND a.currency='IDR';" | tr -d '[:space:]')"
[ "$((fee_after - fee_before))" = "500" ] \
	&& ok "fee[platform] increased by exactly 500 IDR (the quoted fee)" \
	|| fail "fee[platform] delta was $((fee_after - fee_before)), expected 500"

# ─── 5. Idempotent replay: same key, quote already consumed ───────────────────

log "=== 5. Idempotent replay of the same money_in key must NOT double-charge ==="

replay_code="$(curl_internal -sS -o "$WORK_DIR/topup_replay.json" -w '%{http_code}' \
	-X POST "https://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
	-H "Authorization: Bearer $USER_TOKEN" -H "Content-Type: application/json" \
	-d "{\"idempotency_key\":\"topup-fee-1-$RUN_ID\",\"type\":\"money_in\",\
\"amount\":\"100000\",\"quote_id\":\"$QUOTE_ID\",\"metadata\":{\"gateway\":\"bca\"}}")"
replay_body="$(cat "$WORK_DIR/topup_replay.json")"
fee_after_replay="$(psql_exec "$LEDGER_DB_NAME" -c "
	SELECT COALESCE(b.balance,0) FROM accounts a
	JOIN account_balances b ON b.account_id=a.id
	WHERE a.owner_type='system' AND a.type='fee'
	  AND a.system_qualifier='platform' AND a.currency='IDR';" | tr -d '[:space:]')"
# Idempotent replay: either 201 with same transaction OR 200/202 ErrAlreadyPosted.
# In both cases the fee must NOT increase again.
[ "$fee_after_replay" = "$fee_after" ] \
	&& ok "fee unchanged after idempotent replay (code=$replay_code) — no double-charge" \
	|| fail "fee increased on idempotent replay (before=$fee_after after=$fee_after_replay) — double-charge bug"

# ─── 6. QUOTE_EXPIRED: consumed quote rejected on a fresh idempotency key ─────

log "=== 6. Reusing the consumed quote with a different key must return QUOTE_EXPIRED ==="

reuse_resp="$(curl_internal -sS -w '\n%{http_code}' \
	-X POST "https://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
	-H "Authorization: Bearer $USER_TOKEN" -H "Content-Type: application/json" \
	-d "{\"idempotency_key\":\"topup-fee-reuse-$RUN_ID\",\"type\":\"money_in\",\
\"amount\":\"100000\",\"quote_id\":\"$QUOTE_ID\",\"metadata\":{\"gateway\":\"bca\"}}")"
reuse_code="$(echo "$reuse_resp" | tail -1)"
echo "$reuse_resp" | grep -q 'QUOTE_EXPIRED' && [ "$reuse_code" = "422" ] \
	&& ok "consumed quote rejected with 422 QUOTE_EXPIRED — single-use enforced" \
	|| fail "expected 422 QUOTE_EXPIRED, got code=$reuse_code body=${reuse_resp%$'\n'*}"

# ─── 7. Ledger integrity ──────────────────────────────────────────────────────

log "=== 7. Ledger integrity assertions ==="

assert_ledger_balanced
assert_no_inconsistent_projections
assert_no_stuck_pending_transactions

if [ "${FAILED:-0}" -ne 0 ]; then
	exit 1
fi
log "topup-fee-e2e completed"
