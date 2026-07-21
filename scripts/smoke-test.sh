#!/usr/bin/env bash
# End-to-end smoke test against a real, live server (docker-compose
# Postgres/Redis/RabbitMQ + a freshly built binary) — the same manual curl
# walkthrough every doc's "Verifikasi akhir" section has asked for since
# docs/plan/12, now checked in once instead of hand-typed from scratch each
# time a feature needs re-verifying.
#
# Requires: Docker running, this repo checked out, go toolchain available,
# openssl (for HMAC-signing the payin webhook request).
# Does NOT require the app to already be running — this script builds and
# manages its own server process and docker-compose dependencies.
#
# Usage:
#   ./scripts/smoke-test.sh            # run everything
#   ./scripts/smoke-test.sh ledger     # ledger-only: money_in, transfer, withdraw hold/settle/cancel
#   ./scripts/smoke-test.sh payin      # payin-only: mockvendor webhook (valid/bad-signature/unknown-vendor)
#   ./scripts/smoke-test.sh payout     # payout-only: create/get/ownership/admin list/cancel
#
# Shared bootstrap lives in scripts/lib.sh (same helpers scripts/chaos-test.sh
# uses) — extend THAT file, not this one, if the bootstrap sequence itself
# needs to change (new env var, new migration step, etc).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="smoke"
LIB_WORK_DIR_PREFIX="smoke"
APP_PORT="${APP_PORT:-18080}"
INTERNAL_PORT="${INTERNAL_PORT:-18081}"
# shellcheck source=scripts/lib.sh
source "$ROOT_DIR/scripts/lib.sh"

trap cleanup EXIT

# docs/plan/44 K6: same env var name lib.sh's start_*_service functions
# export to the service processes — a CI-generated VENDOR_MOCKVENDOR_SECRET
# must sign AND verify with the identical value, not silently diverge
# because this script's own webhook-signing copy had a different name.
MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"
# Keep business identities stable for easy inspection, but make every
# idempotency/event key unique per invocation so a reused development volume
# cannot turn a valid replay into a false smoke failure.
SMOKE_RUN_ID="${SMOKE_RUN_ID:-$(uuidgen | tr '[:upper:]' '[:lower:]')}"

# json_field extracts "key":"value" (string fields only) from a JSON blob
# read on stdin — good enough for this script's flat response shapes,
# avoids adding a jq dependency just for smoke testing.
json_field() {
	sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"
}

# ─── Section: ledger core (money_in, transfer, withdraw hold/settle/cancel) ─

smoke_ledger() {
	log "=== Ledger: money_in, transfer_p2p, withdraw hold/settle/cancel ==="

	local sender="c0de0000-0000-0000-0000-0000000000a1"
	local recipient="c0de0000-0000-0000-0000-0000000000a2"
	provision_user "$sender" >/dev/null
	provision_hold_account "$sender"
	provision_user "$recipient" >/dev/null
	fund_user "$sender" 1000000
	local token
	token="$(gen_token "$sender")"

	local sender_cash
	sender_cash="$(cash_account_id "$sender")"

	log "GET /api/v1/ledger/accounts/{id}/balance after funding..."
	local balance_json
	balance_json="$(curl -s "http://localhost:$APP_PORT/api/v1/ledger/accounts/$sender_cash/balance" -H "Authorization: Bearer $token")"
	local balance
	balance="$(echo "$balance_json" | json_field balance)"
	[ "$balance" = "1000000" ] && ok "balance endpoint reports 1000000 after funding" || fail "balance endpoint reported '$balance', expected 1000000"

	log "POST transfer_p2p sender -> recipient..."
	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"smoke-transfer-$SMOKE_RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"100000\",\"target_user_id\":\"$recipient\"}")
	[ "${code:0:1}" = "2" ] && ok "transfer_p2p posted (code=$code)" || fail "transfer_p2p got $code, expected 2xx"

	log "withdraw_initiate (hold) then withdraw_settle..."
	local hold_key="smoke-withdraw-settle-hold-$SMOKE_RUN_ID"
	curl_internal -s -o /dev/null -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"$hold_key\",\"type\":\"withdraw_initiate\",\"amount\":\"50000\"}"
	local hold_tx_id
	hold_tx_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM ledger_transactions WHERE idempotency_key = '$hold_key';")"
	code=$(curl_internal -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"smoke-withdraw-settle-settle-$SMOKE_RUN_ID\",\"type\":\"withdraw_settle\",\"amount\":\"50000\",\"reference_id\":\"$hold_tx_id\",\"metadata\":{\"gateway\":\"bca\"}}")
	[ "${code:0:1}" = "2" ] && ok "withdraw_settle posted (code=$code)" || fail "withdraw_settle got $code, expected 2xx"

	log "withdraw_initiate (hold) then withdraw_cancel — money must return..."
	local cash_before_cancel
	cash_before_cancel="$(account_balance "$sender_cash")"
	local hold_key2="smoke-withdraw-cancel-hold-$SMOKE_RUN_ID"
	curl_internal -s -o /dev/null -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"$hold_key2\",\"type\":\"withdraw_initiate\",\"amount\":\"30000\"}"
	local hold_tx_id2
	hold_tx_id2="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM ledger_transactions WHERE idempotency_key = '$hold_key2';")"
	code=$(curl_internal -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"smoke-withdraw-cancel-cancel-$SMOKE_RUN_ID\",\"type\":\"withdraw_cancel\",\"amount\":\"30000\",\"reference_id\":\"$hold_tx_id2\"}")
	[ "${code:0:1}" = "2" ] && ok "withdraw_cancel posted (code=$code)" || fail "withdraw_cancel got $code, expected 2xx"
	local cash_after_cancel
	cash_after_cancel="$(account_balance "$sender_cash")"
	[ "$cash_after_cancel" = "$cash_before_cancel" ] && ok "cancelled withdrawal returned the full held amount (balance unchanged: $cash_after_cancel)" \
		|| fail "cash balance changed across hold+cancel: before=$cash_before_cancel after=$cash_after_cancel"
}

# ─── Section: payin webhook ──────────────────────────────────────────────────

smoke_payin() {
	log "=== Payin: mockvendor webhook (valid / bad-signature / unknown-vendor) ==="

	local user_id="c0de0000-0000-0000-0000-0000000000b1"
	provision_user "$user_id" >/dev/null
	local cash
	cash="$(cash_account_id "$user_id")"
	local balance_before
	balance_before="$(account_balance "$cash")"

	local body
	body="{\"event_id\":\"smoke-evt-$SMOKE_RUN_ID\",\"external_ref\":\"smoke-ref-$SMOKE_RUN_ID\",\"user_id\":\"$user_id\",\"amount\":\"75000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
	local sig
	sig="$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$MOCKVENDOR_SECRET" -r | awk '{print $1}')"

	log "POST /webhooks/mockvendor with a valid signature..."
	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: $sig" -H "Content-Type: application/json" -d "$body")
	[ "${code:0:1}" = "2" ] && ok "signed webhook accepted (code=$code)" || fail "signed webhook got $code, expected 2xx"

	local balance_after
	balance_after="$(account_balance "$cash")"
	local expected=$((balance_before + 75000))
	[ "$balance_after" = "$expected" ] && ok "cash balance increased by exactly the webhook amount ($balance_before -> $balance_after)" \
		|| fail "cash balance mismatch after webhook: before=$balance_before after=$balance_after expected=$expected"

	log "POST /webhooks/mockvendor with a bad signature — must be rejected, no side effect..."
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: 0000deadbeef" -H "Content-Type: application/json" \
		-d "{\"event_id\":\"smoke-evt-bad\",\"external_ref\":\"smoke-ref-bad\",\"user_id\":\"$user_id\",\"amount\":\"1000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}")
	[ "$code" = "401" ] && ok "bad signature rejected with 401" || fail "bad signature got $code, expected 401"

	log "POST /webhooks/unknownvendor — must 404..."
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/unknownvendor" \
		-H "X-Mock-Signature: irrelevant" -H "Content-Type: application/json" -d '{}')
	[ "$code" = "404" ] && ok "unknown vendor rejected with 404" || fail "unknown vendor got $code, expected 404"
}

# ─── Section: payout (create/get/ownership/admin) ────────────────────────────

smoke_payout() {
	log "=== Payout: create (instant-settle + async) / get / ownership / admin list+cancel ==="

	local owner="c0de0000-0000-0000-0000-0000000000c1"
	local other_user="c0de0000-0000-0000-0000-0000000000c2"
	provision_user "$owner" >/dev/null
	provision_hold_account "$owner"
	provision_user "$other_user" >/dev/null
	fund_user "$owner" 500000
	local token other_token admin_token
	token="$(gen_token "$owner")"
	other_token="$(gen_token "$other_user")"
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"

	log "POST /api/v1/payout (instant-settle)..."
	local create1
	create1="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"50000","destination":{"bank_code":"014","account_no":"1"}}')"
	local id1
	id1="$(echo "$create1" | json_field id)"
	[ -n "$id1" ] || fail "instant-settle create response unexpected: $create1"
	# docs/plan/45 Task T1: the create response itself only reports
	# 'submitted' (hold+enqueue) — the vendor result always comes from a
	# separate, asynchronous relay dispatch pass now, even in what used to
	# be called "instant-settle" mode.
	wait_for_payout_status "$id1" "settled" 10
	ok "instant-settle payout created and settled via the relay ($id1)"

	log "GET /api/v1/payout/{id} as the owner..."
	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$APP_PORT/api/v1/payout/$id1" -H "Authorization: Bearer $token")
	[ "$code" = "200" ] && ok "owner can read their own payout (code=$code)" || fail "owner GET got $code, expected 200"

	log "GET /api/v1/payout/{id} as a DIFFERENT user — must 404, not 403..."
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$APP_PORT/api/v1/payout/$id1" -H "Authorization: Bearer $other_token")
	[ "$code" = "404" ] && ok "non-owner GET correctly reports 404 (code=$code)" || fail "non-owner GET got $code, expected 404"

	log "POST /api/v1/payout (async, mock_mode=async -> vendor_pending)..."
	local create2
	create2="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"20000","destination":{"bank_code":"014","account_no":"1","mock_mode":"async"}}')"
	local id2
	id2="$(echo "$create2" | json_field id)"
	[ -n "$id2" ] || fail "async create response unexpected: $create2"
	wait_for_payout_status "$id2" "vendor_pending" 10
	ok "async payout dispatched and left vendor_pending ($id2)"

	log "GET /admin/payout/requests?status=vendor_pending (admin)..."
	local list_resp
	list_resp="$(curl_internal -s "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/requests?status=vendor_pending" -H "Authorization: Bearer $admin_token")"
	echo "$list_resp" | grep -q "$id2" && ok "admin list includes the vendor_pending payout" || fail "admin list did not include $id2: $list_resp"

	log "GET /admin/payout/requests as a non-admin — must 403..."
	code=$(curl_internal -s -o /dev/null -w '%{http_code}' "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/requests" -H "Authorization: Bearer $token")
	[ "$code" = "403" ] && ok "non-admin admin-list rejected with 403 (code=$code)" || fail "non-admin admin-list got $code, expected 403"

	log "POST /admin/payout/requests/{id}/cancel (admin) — money must return..."
	code=$(curl_internal -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/requests/$id2/cancel" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"reason":"smoke test manual cancel"}')
	[ "$code" = "200" ] && ok "admin cancel succeeded (code=$code)" || fail "admin cancel got $code, expected 200"

	local confirm
	confirm="$(curl -s "http://localhost:$APP_PORT/api/v1/payout/$id2" -H "Authorization: Bearer $token")"
	local confirm_status
	confirm_status="$(echo "$confirm" | json_field status)"
	[ "$confirm_status" = "cancelled" ] && ok "payout confirmed cancelled after admin action" || fail "payout status after cancel was '$confirm_status', expected 'cancelled'"
}

# ─── Main ────────────────────────────────────────────────────────────────────

ensure_deps_up
build_server
start_services

case "${1:-all}" in
ledger) smoke_ledger ;;
payin) smoke_payin ;;
payout) smoke_payout ;;
all)
	smoke_ledger
	smoke_payin
	smoke_payout
	;;
*)
	echo "Usage: $0 [ledger|payin|payout|all]"
	exit 2
	;;
esac

log "final ledger integrity check..."
assert_ledger_balanced
assert_no_inconsistent_projections

stop_services

echo
if [ "$FAILED" = "0" ]; then
	printf '\033[1;32m=== ALL SMOKE ASSERTIONS PASSED ===\033[0m\n'
	exit 0
else
	printf '\033[1;31m=== ONE OR MORE SMOKE ASSERTIONS FAILED ===\033[0m\n'
	exit 1
fi
