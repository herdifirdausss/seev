#!/usr/bin/env bash
# Focused VendorService boundary drill for docs/roadmap/active/54.
#
# This is intentionally separate from the broad chaos suite: it exercises the
# vendor callback edge with a real top-up intent and asserts the monetary effect
# remains exactly-once across restart, duplicate delivery, and a client-side
# lost-response retry. It is manual/on-demand and never targets production.
#
# Usage:
#   ./scripts/vendor-boundary-chaos.sh restart
#   ./scripts/vendor-boundary-chaos.sh duplicate
#   ./scripts/vendor-boundary-chaos.sh lost-response
#   ./scripts/vendor-boundary-chaos.sh all

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="vendor-chaos"
LIB_WORK_DIR_PREFIX="vendor-chaos"
VENDOR_MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"
export VENDOR_MOCKVENDOR_SECRET
# shellcheck source=scripts/lib.sh
source "$ROOT_DIR/scripts/lib.sh"

trap cleanup EXIT

CASE_USER=""
CASE_CASH=""
CASE_BEFORE=""
CASE_BODY=""
CASE_SIGNATURE=""

prepare_case() {
	local label=$1 amount=$2 token topup reference event_id
	CASE_USER="$(uuidgen | tr '[:upper:]' '[:lower:]')"
	provision_user "$CASE_USER" >/dev/null
	CASE_CASH="$(cash_account_id "$CASE_USER")"
	CASE_BEFORE="$(account_balance "$CASE_CASH")"
	token="$(gen_token "$CASE_USER")"
	topup="$(curl -sS -X POST "http://localhost:$APP_PORT/api/v1/topup" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"amount\":\"$amount\"}")"
	reference="$(printf '%s' "$topup" | json_field reference)"
	[ -n "$reference" ] || { fail "$label: top-up intent creation failed: $topup"; return 1; }
	event_id="vendor-chaos-$label-$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM"
	CASE_BODY="{\"event_id\":\"$event_id\",\"external_ref\":\"$reference\",\"user_id\":\"$CASE_USER\",\"amount\":\"$amount\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
	CASE_SIGNATURE="$(printf '%s' "$CASE_BODY" | openssl dgst -sha256 -hmac "$VENDOR_MOCKVENDOR_SECRET" -r | awk '{print $1}')"
}

post_callback() {
	curl_internal -sS -o /dev/null -w '%{http_code}' -X POST "https://localhost:$VENDOR_APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: $CASE_SIGNATURE" -H "Content-Type: application/json" -d "$CASE_BODY"
}

assert_credit_once() {
	local label=$1 amount=$2 after expected
	after="$(account_balance "$CASE_CASH")"
	expected=$((CASE_BEFORE + amount))
	[ "$after" = "$expected" ] \
		&& ok "$label credited exactly once ($CASE_BEFORE -> $after)" \
		|| fail "$label balance mismatch: before=$CASE_BEFORE after=$after expected=$expected"
}

scenario_restart() {
	log "=== Vendor boundary: restart during callback delivery ==="
	local amount=6100 code tries=12
	prepare_case restart "$amount"
	kill_vendor_hard
	code="$(post_callback || true)"
	case "$code" in
		2*) fail "callback was accepted while VendorService was down (code=$code)" ;;
		*) ok "callback was not acknowledged while VendorService was down (code=$code)" ;;
	esac
	start_vendor_service
	code=000
	while [ "$tries" -gt 0 ]; do
		code="$(post_callback || true)"
		[ "$code" = "200" ] && break
		sleep 1
		tries=$((tries - 1))
	done
	[ "$code" = "200" ] && ok "callback redelivery accepted after VendorService restart" \
		|| fail "callback redelivery returned $code, expected 200"
	assert_credit_once "restart redelivery" "$amount"
}

scenario_duplicate() {
	log "=== Vendor boundary: duplicate callback delivery ==="
	local amount=6200 first second
	prepare_case duplicate "$amount"
	first="$(post_callback || true)"
	second="$(post_callback || true)"
	[ "${first:0:1}" = "2" ] && ok "first callback accepted ($first)" || fail "first callback returned $first"
	[ "${second:0:1}" = "2" ] && ok "duplicate callback replay accepted ($second)" || fail "duplicate callback returned $second"
	assert_credit_once "duplicate delivery" "$amount"
}

scenario_lost_response() {
	log "=== Vendor boundary: client loses HTTP response and retries ==="
	local amount=6300 first second
	prepare_case lost-response "$amount"
	# A very short client deadline models a vendor disconnect after the request
	# was sent. The follow-up uses the same event_id and signature, so it is safe
	# whether the first request committed before the client timed out or not.
	first="$(curl_internal -k -sS --max-time 0.001 -o /dev/null -w '%{http_code}' \
		-X POST "https://localhost:$VENDOR_APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: $CASE_SIGNATURE" -H "Content-Type: application/json" -d "$CASE_BODY" || true)"
	second="$(post_callback || true)"
	[ "$first" != "401" ] && [ "$first" != "403" ] && ok "lost-response first attempt did not fail authentication/policy (code=$first)" \
		|| fail "lost-response first attempt was rejected before processing (code=$first)"
	[ "${second:0:1}" = "2" ] && ok "same callback accepted on retry ($second)" || fail "lost-response retry returned $second"
	assert_credit_once "lost-response retry" "$amount"
}

run_case() {
	case "$1" in
		restart) scenario_restart ;;
		duplicate) scenario_duplicate ;;
		lost-response) scenario_lost_response ;;
		all)
			scenario_restart
			stop_services
			start_services
			scenario_duplicate
			stop_services
			start_services
			scenario_lost_response
			;;
		*)
			printf 'usage: %s restart|duplicate|lost-response|all\n' "$0" >&2
			return 2
			;;
	esac
}

ensure_deps_up
build_server
start_services
run_case "${1:-all}"
assert_ledger_balanced
assert_no_inconsistent_projections

if [ "$FAILED" -ne 0 ]; then
	exit 1
fi
