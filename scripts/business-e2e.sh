#!/usr/bin/env bash
# Full business journey acceptance test (docs/plan/25 Task T6) — the
# operational definition of "the MVP can be run from end user through daily
# ops and produces revenue": two real users register and log in with real
# JWTs (not gentoken), one tops up via a signed vendor webhook, transfers to
# the other for a fee, withdraws for a fee, both get notified, and an
# operator checks the books balance and the platform actually earned the
# expected revenue.
#
# Requires: Docker running, this repo checked out, go toolchain available,
# openssl (for HMAC-signing the payin webhook request).
# Does NOT require the app to already be running — this script builds and
# manages its own server process and docker-compose dependencies, same as
# scripts/smoke-test.sh and scripts/chaos-test.sh.
#
# Usage:
#   ./scripts/business-e2e.sh
#
# Shared bootstrap lives in scripts/lib.sh — extend THAT file, not this one,
# if the bootstrap sequence itself needs to change.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="e2e"
LIB_WORK_DIR_PREFIX="e2e"
APP_PORT="${APP_PORT:-18100}"
INTERNAL_PORT="${INTERNAL_PORT:-18101}"

# Fee pricing for this run — flat fees only, deliberately simple numbers so
# expected revenue is easy to eyeball in the final assertion. Exported
# BEFORE start_server (scripts/lib.sh) so its subshell inherits them, same
# mechanism AUTH_BOOTSTRAP_*/TOPUP_INTENT_TTL below rely on — lib.sh's
# start_server only explicitly exports the connection/infra env vars, but a
# bash subshell always inherits everything already exported in its parent.
export AUTH_BOOTSTRAP_ADMIN_EMAIL="business-e2e-admin@example.com"
export AUTH_BOOTSTRAP_ADMIN_PASSWORD="BootstrapAdmin!2026"
export TOPUP_INTENT_TTL=1h
export DEFAULT_CURRENCY=IDR
# Production default is 60s (docs/plan/17 Task T1) — kyc_journey (docs/plan/39
# Task T6) proves a KYC tier upgrade's new policy_limits cap applies without
# waiting out that window.
export POLICY_CACHE_TTL=2s
# Production default is 5 (docs/plan/40 Task T1) — quote_journey's failover
# drill needs the circuit to trip on the FIRST force-fail-induced Submit
# failure, same rationale as chaos-test.sh scenario 8.
export BREAKER_FAILURE_THRESHOLD=1

# shellcheck source=scripts/lib.sh
source "$ROOT_DIR/scripts/lib.sh"

trap cleanup EXIT

MOCKVENDOR_SECRET="script-test-mockvendor-secret-at-least-32-chars-long"
RUN_ID="$(date +%s)-$$"


# json_field and await_notification now live in scripts/lib.sh (shared by
# every script that curls the JSON API — single source of truth instead of
# drifting copies).

fee_platform_balance() {
	psql_exec "$LEDGER_DB_NAME" -c "
		SELECT COALESCE(b.balance, 0) FROM accounts a
		JOIN account_balances b ON b.account_id = a.id
		WHERE a.owner_type = 'system' AND a.type = 'fee' AND a.system_qualifier = 'platform' AND a.currency = 'IDR';"
}

# ─── Section 1: onboarding — real register + login, not gentoken ────────────

onboard() {
	log "=== 1. Onboarding: register + login 2 real users ==="

	local email_a="e2e-$RUN_ID-a@example.com"
	local email_b="e2e-$RUN_ID-b@example.com"
	local password="BusinessE2E!2026"

	local reg_a
	reg_a="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/register" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"$email_a\",\"password\":\"$password\",\"full_name\":\"User A\"}")"
	USER_A="$(echo "$reg_a" | json_field id)"
	[ -n "$USER_A" ] && ok "user A registered ($USER_A)" || fail "user A registration failed: $reg_a"

	local reg_b
	reg_b="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/register" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"$email_b\",\"password\":\"$password\",\"full_name\":\"User B\"}")"
	USER_B="$(echo "$reg_b" | json_field id)"
	[ -n "$USER_B" ] && ok "user B registered ($USER_B)" || fail "user B registration failed: $reg_b"

	local login_a
	login_a="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/login" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"$email_a\",\"password\":\"$password\"}")"
	TOKEN_A="$(echo "$login_a" | json_field access_token)"
	local refresh_a
	refresh_a="$(echo "$login_a" | json_field refresh_token)"
	[ -n "$TOKEN_A" ] && ok "user A logged in with a real JWT" || fail "user A login failed: $login_a"

	local login_b
	login_b="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/login" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"$email_b\",\"password\":\"$password\"}")"
	TOKEN_B="$(echo "$login_b" | json_field access_token)"
	local refresh_b
	refresh_b="$(echo "$login_b" | json_field refresh_token)"
	[ -n "$TOKEN_B" ] && ok "user B logged in with a real JWT" || fail "user B login failed: $login_b"

	log "clearing the KYC gate for A and B (gotcha #9 master) — every script user that transacts must be L1+ or every gated route 403s..."
	local kyc_resp
	kyc_resp="$(kyc_approve_l1 "$AUTH_APP_PORT" "$TOKEN_A" "$refresh_a")"
	TOKEN_A="$(echo "$kyc_resp" | json_field access_token)"
	[ -n "$TOKEN_A" ] && ok "user A KYC L1 approved, token refreshed" || fail "user A KYC L1 dance failed"
	kyc_resp="$(kyc_approve_l1 "$AUTH_APP_PORT" "$TOKEN_B" "$refresh_b")"
	TOKEN_B="$(echo "$kyc_resp" | json_field access_token)"
	[ -n "$TOKEN_B" ] && ok "user B KYC L1 approved, token refreshed" || fail "user B KYC L1 dance failed"

	log "GET /api/v1/users/me confirms the JWT round-trips through auth middleware..."
	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$AUTH_APP_PORT/api/v1/users/me" -H "Authorization: Bearer $TOKEN_A")
	[ "$code" = "200" ] && ok "GET /users/me succeeds for user A (code=$code)" || fail "GET /users/me got $code, expected 200"

	local cash_a
	cash_a="$(cash_account_id "$USER_A")"
	[ -n "$cash_a" ] && ok "user A's ledger cash account was auto-provisioned on register" \
		|| fail "user A has no cash account — provisioning on register did not happen"

	# Seed DB-backed fee rules through the operator API (docs/plan/33 T4).
	# A receives a per-user override (500) while the global default is 1000.
	# Rule ids for A's transfer override and the withdraw fee are captured
	# as globals (TRANSFER_FEE_RULE_ID/WITHDRAW_FEE_RULE_ID) — section 6's
	# quote journey re-prices them via PUT to prove a quote is honored
	# exactly despite an admin changing fee_rules in between.
	local admin_token fee_url code resp
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"
	fee_url="http://localhost:$LEDGER_INTERNAL_PORT/api/v1/admin/ledger/fee-rules"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$fee_url" -H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"tx_type":"transfer_p2p","currency":"IDR","flat_minor_units":1000,"fee_gateway":"platform"}')
	[ "$code" = "201" ] && ok "seeded global transfer fee via admin API" || fail "global fee seed got HTTP $code"
	resp="$(curl -s -X POST "$fee_url" -H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d "{\"tx_type\":\"transfer_p2p\",\"currency\":\"IDR\",\"user_id\":\"$USER_A\",\"flat_minor_units\":500,\"fee_gateway\":\"platform\"}")"
	TRANSFER_FEE_RULE_ID="$(echo "$resp" | json_field id)"
	[ -n "$TRANSFER_FEE_RULE_ID" ] && ok "seeded per-user transfer override via admin API ($TRANSFER_FEE_RULE_ID)" || fail "per-user fee seed failed: $resp"
	resp="$(curl -s -X POST "$fee_url" -H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"tx_type":"withdraw_settle","currency":"IDR","flat_minor_units":2000,"fee_gateway":"platform"}')"
	WITHDRAW_FEE_RULE_ID="$(echo "$resp" | json_field id)"
	[ -n "$WITHDRAW_FEE_RULE_ID" ] && ok "seeded withdraw fee via admin API ($WITHDRAW_FEE_RULE_ID)" || fail "withdraw fee seed failed: $resp"
}

# ─── Section 2: top-up via signed vendor webhook ─────────────────────────────

topup() {
	log "=== 2. Top-up: create intent -> signed mockvendor webhook -> balance + notification ==="
	local admin_token route_json route_id
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"
	psql_exec "$PAYIN_DB_NAME" -c "DELETE FROM payin_routing_rules WHERE priority = 10;" >/dev/null
	curl -s -o /dev/null -X PUT "http://localhost:$PAYIN_ADMIN_PORT/admin/payin/vendor-gateways/mockvendor" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"gateway":"bca"}'
	route_json="$(curl -s -X POST "http://localhost:$PAYIN_ADMIN_PORT/admin/payin/routing-rules" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"flow":"topup","priority":10,"enabled":true,"currency":"IDR","vendor":"mockvendor"}')"
	route_id="$(echo "$route_json" | json_field id)"
	[ -n "$route_id" ] && ok "admin created DB routing rule ($route_id)" || fail "routing rule creation failed: $route_json"

	local create
	create="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/topup" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"amount":"500000"}')"
	local intent_id reference
	intent_id="$(echo "$create" | json_field id)"
	reference="$(echo "$create" | json_field reference)"
	[ -n "$intent_id" ] && [ -n "$reference" ] && ok "topup intent created (id=$intent_id reference=$reference)" \
		|| fail "topup intent creation failed: $create"

	local body
	body="{\"event_id\":\"e2e-topup-$RUN_ID\",\"external_ref\":\"$reference\",\"user_id\":\"$(uuidgen | tr '[:upper:]' '[:lower:]')\",\"amount\":\"500000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
	local sig
	sig="$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$MOCKVENDOR_SECRET" -r | awk '{print $1}')"
	log "webhook payload deliberately carries a RANDOM user_id — the intent's OWN user_id must win, proving the vendor never learns it..."
	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: $sig" -H "Content-Type: application/json" -d "$body")
	[ "${code:0:1}" = "2" ] && ok "signed topup webhook accepted (code=$code)" || fail "topup webhook got $code, expected 2xx"

	local cash_a balance_a
	cash_a="$(cash_account_id "$USER_A")"
	balance_a="$(account_balance "$cash_a")"
	[ "$balance_a" = "500000" ] && ok "user A's balance is 500000 after top-up" \
		|| fail "user A balance after top-up was '$balance_a', expected 500000"

	local intent_status
	intent_status="$(curl -s "http://localhost:$APP_PORT/api/v1/topup/$intent_id" -H "Authorization: Bearer $TOKEN_A" | json_field status)"
	[ "$intent_status" = "settled" ] && ok "topup intent status is 'settled'" || fail "topup intent status was '$intent_status', expected 'settled'"

	log "disable every matching route via admin API — create must return 422 NO_ROUTE..."
	local disabled_payload='{"flow":"topup","priority":10,"enabled":false,"currency":"IDR","vendor":"mockvendor"}'
	curl -s -o /dev/null -X PUT "http://localhost:$PAYIN_ADMIN_PORT/admin/payin/routing-rules/$route_id" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d "$disabled_payload"
	curl -s -o /dev/null -X PUT "http://localhost:$PAYIN_ADMIN_PORT/admin/payin/routing-rules/00000000-0000-7000-8000-000000000029" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"flow":"topup","priority":1000,"enabled":false,"vendor":"mockvendor"}'
	local no_route
	no_route="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/topup" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -d '{"amount":"1000"}')"
	echo "$no_route" | grep -q '"code":"NO_ROUTE"' && [ "$(echo "$no_route" | tail -1)" = "422" ] \
		&& ok "disabled rules return 422 NO_ROUTE" || fail "expected 422 NO_ROUTE, got: $no_route"
	curl -s -o /dev/null -X PUT "http://localhost:$PAYIN_ADMIN_PORT/admin/payin/routing-rules/$route_id" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"flow":"topup","priority":10,"enabled":true,"currency":"IDR","vendor":"mockvendor"}'

	await_notification "$TOKEN_A" "Dana masuk" || true
}

# ─── Section 3: KYC gate + tier journey (docs/plan/39) ───────────────────────

kyc_journey() {
	log "=== 3. KYC journey: gate blocks L0, L1 unlocks small transfers, L2 unlocks large ones ==="

	local email_c="e2e-$RUN_ID-c@example.com"
	local email_d="e2e-$RUN_ID-d@example.com"
	local password="BusinessE2E!2026"

	local reg_c reg_d
	reg_c="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/register" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"$email_c\",\"password\":\"$password\",\"full_name\":\"User C\"}")"
	USER_C="$(echo "$reg_c" | json_field id)"
	[ -n "$USER_C" ] && ok "user C registered ($USER_C)" || fail "user C registration failed: $reg_c"

	# D is a passive recipient ONLY — never posts anything itself, so it
	# never needs its own KYC dance (the gate/policy engine both key off the
	# CALLER, not the recipient).
	reg_d="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/register" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"$email_d\",\"password\":\"$password\",\"full_name\":\"User D\"}")"
	USER_D="$(echo "$reg_d" | json_field id)"
	[ -n "$USER_D" ] && ok "user D registered ($USER_D)" || fail "user D registration failed: $reg_d"

	local login_c refresh_c
	login_c="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/login" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"$email_c\",\"password\":\"$password\"}")"
	TOKEN_C="$(echo "$login_c" | json_field access_token)"
	refresh_c="$(echo "$login_c" | json_field refresh_token)"
	[ -n "$TOKEN_C" ] && ok "user C logged in with a real JWT (kyc_level=0, brand new)" || fail "user C login failed: $login_c"

	log "user C (L0) attempts a transfer — must reject with 403 KYC_REQUIRED, before any balance/policy check..."
	local blocked_resp blocked_code
	blocked_resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_C" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-kyc-blocked-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"10000\",\"target_user_id\":\"$USER_D\"}")"
	blocked_code="$(echo "$blocked_resp" | tail -1)"
	[ "$blocked_code" = "403" ] && echo "$blocked_resp" | grep -q "KYC_REQUIRED" \
		&& ok "L0 transfer rejected with 403 KYC_REQUIRED" \
		|| fail "expected 403 KYC_REQUIRED, got: $blocked_resp"

	log "submitting L1 (mock auto-approves) and refreshing the token..."
	local kyc_resp
	kyc_resp="$(kyc_approve_l1 "$AUTH_APP_PORT" "$TOKEN_C" "$refresh_c")"
	TOKEN_C="$(echo "$kyc_resp" | json_field access_token)"
	refresh_c="$(echo "$kyc_resp" | json_field refresh_token)"
	[ -n "$TOKEN_C" ] && ok "user C KYC L1 approved, token refreshed" || fail "user C KYC L1 dance failed"

	log "topping up user C so there is real money to move (topup route already active from section 2)..."
	local create intent_id reference
	create="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/topup" \
		-H "Authorization: Bearer $TOKEN_C" -H "Content-Type: application/json" \
		-d '{"amount":"3000000"}')"
	intent_id="$(echo "$create" | json_field id)"
	reference="$(echo "$create" | json_field reference)"
	[ -n "$intent_id" ] && [ -n "$reference" ] && ok "user C topup intent created post-L1 (id=$intent_id)" \
		|| fail "user C topup intent creation failed: $create"

	local body sig code
	body="{\"event_id\":\"e2e-kyc-topup-$RUN_ID\",\"external_ref\":\"$reference\",\"user_id\":\"$(uuidgen | tr '[:upper:]' '[:lower:]')\",\"amount\":\"3000000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
	sig="$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$MOCKVENDOR_SECRET" -r | awk '{print $1}')"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: $sig" -H "Content-Type: application/json" -d "$body")
	[ "${code:0:1}" = "2" ] && ok "user C topup webhook accepted (code=$code)" || fail "user C topup webhook got $code, expected 2xx"

	log "L1 covers a small transfer (well under the 1,000,000 max_per_tx cap)..."
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_C" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-kyc-small-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"50000\",\"target_user_id\":\"$USER_D\"}")
	[ "${code:0:1}" = "2" ] && ok "L1 small transfer (50000) posted (code=$code)" || fail "L1 small transfer got $code, expected 2xx"

	log "a transfer ABOVE L1's 1,000,000 max_per_tx cap must reject with 422 policy limit..."
	local limit_resp limit_code
	limit_resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_C" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-kyc-over-cap-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"2000000\",\"target_user_id\":\"$USER_D\"}")"
	limit_code="$(echo "$limit_resp" | tail -1)"
	[ "$limit_code" = "422" ] && echo "$limit_resp" | grep -q "policy limit exceeded" \
		&& ok "over-cap transfer rejected with 422 policy limit exceeded" \
		|| fail "expected 422 policy limit exceeded, got: $limit_resp"

	log "submitting L2 (mock ALWAYS refers) and having an admin approve it..."
	local admin_token
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"
	kyc_resp="$(kyc_submit_l2_and_admin_approve "$AUTH_APP_PORT" "$AUTH_INTERNAL_PORT" "$admin_token" "$TOKEN_C" "$refresh_c")"
	TOKEN_C="$(echo "$kyc_resp" | json_field access_token)"
	[ -n "$TOKEN_C" ] && ok "user C KYC L2 approved by admin, token refreshed" || fail "user C KYC L2 dance failed"

	# ApplyKycTier upserts policy_limits immediately, but ledger-service's
	# own in-process policy engine cache (POLICY_CACHE_TTL=2s for this run,
	# vs 60s production default) may still hold C's pre-upgrade L1 limit
	# from the over-cap check above — wait it out rather than flake.
	sleep 3

	log "the SAME over-L1-cap transfer now succeeds under L2's 100,000,000 cap..."
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_C" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-kyc-l2-large-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"2000000\",\"target_user_id\":\"$USER_D\"}")
	[ "${code:0:1}" = "2" ] && ok "L2 large transfer (2000000) posted (code=$code)" || fail "L2 large transfer got $code, expected 2xx"
}

# ─── Section 4: fee-charging P2P transfer ────────────────────────────────────

transfer() {
	log "=== 4. Transfer P2P (with platform fee) ==="

	local cash_a cash_b fee_before
	cash_a="$(cash_account_id "$USER_A")"
	cash_b="$(cash_account_id "$USER_B")"
	fee_before="$(fee_platform_balance)"

	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-transfer-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"100000\",\"target_user_id\":\"$USER_B\"}")
	[ "${code:0:1}" = "2" ] && ok "transfer_p2p posted (code=$code)" || fail "transfer_p2p got $code, expected 2xx"

	local balance_a balance_b fee_after
	balance_a="$(account_balance "$cash_a")"
	balance_b="$(account_balance "$cash_b")"
	fee_after="$(fee_platform_balance)"

	[ "$balance_a" = "400000" ] && ok "sender A debited the full 100000 (balance now 400000)" \
		|| fail "sender A balance was '$balance_a', expected 400000"
	[ "$balance_b" = "99500" ] && ok "recipient B credited 100000 minus A's 500 override (balance now 99500)" \
		|| fail "recipient B balance was '$balance_b', expected 99500"
	[ "$((fee_after - fee_before))" = "500" ] && ok "fee[platform] increased by exactly A's 500 per-user fee" \
		|| fail "fee[platform] changed by $((fee_after - fee_before)), expected 500"

	await_notification "$TOKEN_A" "Transfer terkirim" || true
	await_notification "$TOKEN_B" "Transfer diterima" || true
}

# ─── Section 5: fee-charging withdraw (settled) + fee-free cancel ────────────

withdraw() {
	log "=== 5. Withdraw (with platform fee), then a separate cancelled withdraw charges NO fee ==="
	local admin_token route_json route_id
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"
	psql_exec "$PAYOUT_DB_NAME" -c "DELETE FROM payout_routing_rules WHERE priority = 10;" >/dev/null
	curl -s -o /dev/null -X PUT "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendor-gateways/mockvendor" -H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"gateway":"bca"}'
	route_json="$(curl -s -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/routing-rules" -H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"flow":"payout","priority":10,"enabled":true,"currency":"IDR","vendor":"mockvendor"}')"
	route_id="$(echo "$route_json" | json_field id)"
	[ -n "$route_id" ] && ok "admin created payout DB routing rule ($route_id)" || fail "payout routing rule creation failed: $route_json"

	local cash_a fee_before
	cash_a="$(cash_account_id "$USER_A")"
	fee_before="$(fee_platform_balance)"
	local balance_before
	balance_before="$(account_balance "$cash_a")"

	local create
	create="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"amount":"200000","destination":{"bank_code":"014","account_no":"1"}}')"
	local payout_id status
	payout_id="$(echo "$create" | json_field id)"
	status="$(echo "$create" | json_field status)"
	[ -n "$payout_id" ] && [ "$status" = "settled" ] && ok "withdraw created and settled instantly ($payout_id)" \
		|| fail "withdraw create response unexpected: $create"

	local balance_after fee_after
	balance_after="$(account_balance "$cash_a")"
	fee_after="$(fee_platform_balance)"
	local expected_balance=$((balance_before - 200000))
	[ "$balance_after" = "$expected_balance" ] && ok "cash debited the full 200000 withdrawn (balance now $balance_after)" \
		|| fail "cash balance after withdraw was '$balance_after', expected $expected_balance"
	[ "$((fee_after - fee_before))" = "2000" ] && ok "fee[platform] increased by exactly the 2000 withdraw fee" \
		|| fail "fee[platform] changed by $((fee_after - fee_before)), expected 2000"

	await_notification "$TOKEN_A" "Withdraw berhasil" || true

	log "a SEPARATE async withdraw, then admin-cancelled — must refund in full and charge NO fee..."
	local fee_before_cancel balance_before_cancel
	fee_before_cancel="$(fee_platform_balance)"
	balance_before_cancel="$(account_balance "$cash_a")"

	local create2
	create2="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"amount":"30000","destination":{"bank_code":"014","account_no":"1","mock_mode":"async"}}')"
	local payout_id2 status2
	payout_id2="$(echo "$create2" | json_field id)"
	status2="$(echo "$create2" | json_field status)"
	[ -n "$payout_id2" ] && [ "$status2" = "vendor_pending" ] && ok "async withdraw created and left vendor_pending ($payout_id2)" \
		|| fail "async withdraw create response unexpected: $create2"

	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/requests/$payout_id2/cancel" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"reason":"business-e2e cancel"}')
	[ "$code" = "200" ] && ok "admin cancel succeeded (code=$code)" || fail "admin cancel got $code, expected 200"

	local balance_after_cancel fee_after_cancel
	balance_after_cancel="$(account_balance "$cash_a")"
	fee_after_cancel="$(fee_platform_balance)"
	[ "$balance_after_cancel" = "$balance_before_cancel" ] && ok "cancelled withdraw refunded in full (balance unchanged: $balance_after_cancel)" \
		|| fail "cash balance changed across the cancelled withdraw: before=$balance_before_cancel after=$balance_after_cancel"
	[ "$fee_after_cancel" = "$fee_before_cancel" ] && ok "cancelled withdraw charged NO fee (fee[platform] unchanged)" \
		|| fail "fee[platform] changed on a cancelled withdraw: before=$fee_before_cancel after=$fee_after_cancel"

	await_notification "$TOKEN_A" "Withdraw dibatalkan" || true
}

# ─── Section 6: fee quote journey (docs/plan/38) ─────────────────────────────

quote_journey() {
	log "=== 6. Fee quote journey: honored exactly, tamper detection, single-use, payout quote ==="

	local admin_token
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"

	log "quoting a transfer_p2p (prices A's 500 per-user override)..."
	local quote_resp quote_id fee_amount
	quote_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/ledger/fees/quote" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"transaction_type":"transfer_p2p","amount":"50000"}')"
	quote_id="$(echo "$quote_resp" | json_field quote_id)"
	fee_amount="$(echo "$quote_resp" | json_field fee_amount)"
	[ -n "$quote_id" ] && [ "$fee_amount" = "500" ] && ok "quote created (id=$quote_id fee=$fee_amount)" \
		|| fail "quote creation unexpected: $quote_resp"

	log "admin re-prices A's transfer_p2p override to 9000 AFTER the quote was created..."
	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/admin/ledger/fee-rules/$TRANSFER_FEE_RULE_ID" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d "{\"tx_type\":\"transfer_p2p\",\"currency\":\"IDR\",\"user_id\":\"$USER_A\",\"flat_minor_units\":9000,\"fee_gateway\":\"platform\"}")
	[ "$code" = "200" ] && ok "fee_rules re-priced to 9000 via admin API" || fail "fee rule update got $code, expected 200"

	local fee_before
	fee_before="$(fee_platform_balance)"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-quote-transfer-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"50000\",\"target_user_id\":\"$USER_B\",\"quote_id\":\"$quote_id\"}")
	[ "${code:0:1}" = "2" ] && ok "quote-backed transfer posted (code=$code)" || fail "quote-backed transfer got $code, expected 2xx"

	local fee_after
	fee_after="$(fee_platform_balance)"
	[ "$((fee_after - fee_before))" = "500" ] && ok "fee[platform] increased by EXACTLY the quoted 500, not the changed 9000 rule" \
		|| fail "fee[platform] changed by $((fee_after - fee_before)), expected 500 (quote must be honored despite rule change)"

	log "tampering the amount after quoting must reject with 422 QUOTE_MISMATCH, WITHOUT burning the quote..."
	local quote2_resp quote2_id
	quote2_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/ledger/fees/quote" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"transaction_type":"transfer_p2p","amount":"20000"}')"
	quote2_id="$(echo "$quote2_resp" | json_field quote_id)"
	[ -n "$quote2_id" ] && ok "second quote created for the mismatch test (id=$quote2_id)" || fail "second quote creation failed: $quote2_resp"

	local mismatch_resp mismatch_code
	mismatch_resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-quote-mismatch-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"99999\",\"target_user_id\":\"$USER_B\",\"quote_id\":\"$quote2_id\"}")"
	mismatch_code="$(echo "$mismatch_resp" | tail -1)"
	echo "$mismatch_resp" | grep -q '\[QUOTE_MISMATCH\]' && [ "$mismatch_code" = "422" ] \
		&& ok "tampered amount rejected with 422 QUOTE_MISMATCH" \
		|| fail "expected 422 QUOTE_MISMATCH, got: $mismatch_resp"

	log "the SAME quote survived the mismatch attempt — posting the CORRECT amount now succeeds..."
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-quote2-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"20000\",\"target_user_id\":\"$USER_B\",\"quote_id\":\"$quote2_id\"}")
	[ "${code:0:1}" = "2" ] && ok "quote consumed successfully after surviving the mismatch attempt (code=$code)" \
		|| fail "correct-amount post after mismatch got $code, expected 2xx"

	log "consuming the SAME quote again must reject with 422 QUOTE_EXPIRED (single-use)..."
	local reuse_resp reuse_code
	reuse_resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-quote-reuse-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"20000\",\"target_user_id\":\"$USER_B\",\"quote_id\":\"$quote2_id\"}")"
	reuse_code="$(echo "$reuse_resp" | tail -1)"
	echo "$reuse_resp" | grep -q '\[QUOTE_EXPIRED\]' && [ "$reuse_code" = "422" ] \
		&& ok "reusing an already-consumed quote rejected with 422 QUOTE_EXPIRED" \
		|| fail "expected 422 QUOTE_EXPIRED, got: $reuse_resp"

	log "re-quoting fresh succeeds..."
	local quote3_resp quote3_id
	quote3_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/ledger/fees/quote" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"transaction_type":"transfer_p2p","amount":"10000"}')"
	quote3_id="$(echo "$quote3_resp" | json_field quote_id)"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"e2e-quote3-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"10000\",\"target_user_id\":\"$USER_B\",\"quote_id\":\"$quote3_id\"}")
	[ -n "$quote3_id" ] && [ "${code:0:1}" = "2" ] && ok "re-quote after rejection/consumption succeeds (code=$code)" \
		|| fail "re-quote journey failed: quote_id=$quote3_id code=$code"

	log "payout ber-quote: fee charged at settle = quote, even though fee_rules changes mid-flight..."
	local payout_quote_resp payout_quote_id payout_fee_amount
	payout_quote_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/ledger/fees/quote" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"transaction_type":"withdraw_settle","amount":"40000"}')"
	payout_quote_id="$(echo "$payout_quote_resp" | json_field quote_id)"
	payout_fee_amount="$(echo "$payout_quote_resp" | json_field fee_amount)"
	[ -n "$payout_quote_id" ] && [ "$payout_fee_amount" = "2000" ] && ok "payout quote created (id=$payout_quote_id fee=$payout_fee_amount)" \
		|| fail "payout quote creation unexpected: $payout_quote_resp"

	code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/admin/ledger/fee-rules/$WITHDRAW_FEE_RULE_ID" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"tx_type":"withdraw_settle","currency":"IDR","flat_minor_units":8000,"fee_gateway":"platform"}')
	[ "$code" = "200" ] && ok "withdraw_settle re-priced to 8000 via admin API" || fail "fee rule update got $code, expected 200"

	local fee_before_payout
	fee_before_payout="$(fee_platform_balance)"
	local payout_create payout_status
	payout_create="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"amount\":\"40000\",\"destination\":{\"bank_code\":\"014\",\"account_no\":\"1\"},\"quote_id\":\"$payout_quote_id\"}")"
	payout_status="$(echo "$payout_create" | json_field status)"
	[ "$payout_status" = "settled" ] && ok "quote-backed payout settled instantly" || fail "quote-backed payout unexpected: $payout_create"

	local fee_after_payout
	fee_after_payout="$(fee_platform_balance)"
	[ "$((fee_after_payout - fee_before_payout))" = "2000" ] && ok "fee[platform] increased by EXACTLY the quoted 2000, not the changed 8000 rule" \
		|| fail "fee[platform] changed by $((fee_after_payout - fee_before_payout)), expected 2000 (payout quote honored despite mid-flight rule change)"

	log "payout ber-quote + drill failover (docs/plan/40): force-fail mockvendor -> quote-backed payout routes to mockvendor2..."
	local failover_quote_resp failover_quote_id failover_fee_amount
	failover_quote_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/ledger/fees/quote" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"transaction_type":"withdraw_settle","amount":"25000"}')"
	failover_quote_id="$(echo "$failover_quote_resp" | json_field quote_id)"
	failover_fee_amount="$(echo "$failover_quote_resp" | json_field fee_amount)"
	[ -n "$failover_quote_id" ] && ok "failover-drill quote created (id=$failover_quote_id fee=$failover_fee_amount)" \
		|| fail "failover-drill quote creation unexpected: $failover_quote_resp"

	# mockvendor2 seeded as a FALLBACK behind mockvendor's existing priority-10
	# rule (from section 5) — priority 11 is a LARGER number, tried SECOND,
	# since docs/plan/40 Task T2's ResolveCandidates orders priority ASC
	# (smallest number wins first; see CLAUDE.md's debugging notes).
	curl -s -o /dev/null -X PUT "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendor-gateways/mockvendor2" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"gateway":"gopay"}'
	psql_exec "$PAYOUT_DB_NAME" -c "DELETE FROM payout_routing_rules WHERE priority = 11;" >/dev/null
	local mv2_route_json mv2_route_id
	mv2_route_json="$(curl -s -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/routing-rules" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"flow":"payout","priority":11,"enabled":true,"vendor":"mockvendor2"}')"
	mv2_route_id="$(echo "$mv2_route_json" | json_field id)"
	[ -n "$mv2_route_id" ] && ok "admin seeded mockvendor2 as fallback routing rule ($mv2_route_id)" \
		|| fail "mockvendor2 routing rule creation failed: $mv2_route_json"

	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":true}'

	log "tripping mockvendor's circuit with a small probe payout (no quote — stays pinned/uncertain by the anti-double-payout rule, by design)..."
	local probe_resp probe_id probe_vendor probe_status
	probe_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"amount":"1000","destination":{"bank_code":"014","account_no":"9"}}')"
	probe_id="$(echo "$probe_resp" | json_field id)"
	[ -n "$probe_id" ] && ok "probe payout created ($probe_id)" || fail "probe payout create failed: $probe_resp"
	probe_vendor="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT vendor FROM payout_requests WHERE id='$probe_id';")"
	probe_status="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id='$probe_id';")"
	[ "$probe_vendor" = "mockvendor" ] && [ "$probe_status" = "submitted" ] \
		&& ok "probe payout pinned to mockvendor, uncertain (trips the circuit)" \
		|| fail "probe payout unexpected vendor='$probe_vendor' status='$probe_status'"

	local health_resp
	health_resp="$(curl -s "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/health" -H "Authorization: Bearer $admin_token")"
	echo "$health_resp" | grep -q '"vendor":"mockvendor","state":"open"' \
		&& ok "admin vendor health confirms mockvendor's circuit is open" \
		|| fail "admin vendor health did not report mockvendor open: $health_resp"

	local failover_before_fee
	failover_before_fee="$(fee_platform_balance)"
	local failover_payout_resp
	FAILOVER_REQUEST_ID="e2e-payout-trace-$RUN_ID"
	failover_payout_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-H "X-Request-Id: $FAILOVER_REQUEST_ID" \
		-d "{\"amount\":\"25000\",\"destination\":{\"bank_code\":\"014\",\"account_no\":\"1\"},\"quote_id\":\"$failover_quote_id\"}")"
	FAILOVER_PAYOUT_ID="$(echo "$failover_payout_resp" | json_field id)"
	local failover_payout_status failover_payout_vendor
	failover_payout_status="$(echo "$failover_payout_resp" | json_field status)"
	failover_payout_vendor="$(echo "$failover_payout_resp" | json_field vendor)"
	[ "$failover_payout_vendor" = "mockvendor2" ] && [ "$failover_payout_status" = "settled" ] \
		&& ok "quote-backed payout routed straight to mockvendor2 (mockvendor's circuit is open) and settled ($FAILOVER_PAYOUT_ID)" \
		|| fail "failover payout unexpected: $failover_payout_resp"

	local failover_after_fee
	failover_after_fee="$(fee_platform_balance)"
	[ "$((failover_after_fee - failover_before_fee))" = "$failover_fee_amount" ] \
		&& ok "failover payout charged EXACTLY the stored quote fee ($failover_fee_amount)" \
		|| fail "fee[platform] changed by $((failover_after_fee - failover_before_fee)), expected $failover_fee_amount"

	log "recovering mockvendor..."
	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":false}'
}

# ─── Section 7: daily ops — integrity + revenue + operator visibility ────────

ops() {
	log "=== 7. Daily ops: ledger integrity, revenue, operator tooling ==="

	assert_ledger_balanced
	assert_no_inconsistent_projections
	assert_no_stuck_pending_transactions

	local admin_token
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"

	log "GET /admin/reports/position (operator revenue visibility)..."
	local from to code
	from="$(date -u +%Y-%m-%d)"
	to="$(date -u +%Y-%m-%d)"
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/reports/position?from=$from&to=$to" \
		-H "Authorization: Bearer $admin_token")
	[ "$code" = "200" ] && ok "admin daily position report reachable (code=$code)" || fail "admin position report got $code, expected 200"

	log "GET /admin/outbox/dead — must be empty after a clean happy-path run..."
	local dead_resp
	dead_resp="$(curl -s "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/outbox/dead" -H "Authorization: Bearer $admin_token")"
	if echo "$dead_resp" | grep -q '"events":\[\]' || echo "$dead_resp" | grep -q '"events":null'; then
		ok "outbox dead-letter list is empty"
	else
		fail "outbox dead-letter list was not empty: $dead_resp"
	fi

	log "GET /admin/recon/batches — operator tooling reachable without SQL..."
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/recon/batches" \
		-H "Authorization: Bearer $admin_token")
	[ "$code" = "200" ] && ok "admin recon batch list reachable (code=$code)" || fail "admin recon batch list got $code, expected 200"

	log "GET /admin/recon/batches as a non-admin — must 403..."
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/recon/batches" \
		-H "Authorization: Bearer $TOKEN_A")
	[ "$code" = "403" ] && ok "non-admin rejected from recon batch list (code=$code)" || fail "non-admin got $code, expected 403"

	log "GET /admin/ledger/fee-rules — operator sees the pricing seeded in section 1 without SQL..."
	local fee_rules_resp
	fee_rules_resp="$(curl -s "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/admin/ledger/fee-rules" -H "Authorization: Bearer $admin_token")"
	echo "$fee_rules_resp" | grep -q '"tx_type":"transfer_p2p"' \
		&& ok "fee-rules admin surface shows the configured transfer_p2p pricing" \
		|| fail "fee-rules admin surface missing expected rules: $fee_rules_resp"

	log "GET /admin/fraud/events (fraud-service) — operator tooling reachable without SQL..."
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$FRAUD_ADMIN_PORT/api/v1/admin/fraud/events" \
		-H "Authorization: Bearer $admin_token")
	[ "$code" = "200" ] && ok "admin fraud events list reachable (code=$code)" || fail "admin fraud events list got $code, expected 200"

	log "GET /admin/kyc/submissions?status=pending (auth-service internal) — operator tooling reachable without SQL..."
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$AUTH_INTERNAL_PORT/api/v1/admin/kyc/submissions?status=pending" \
		-H "Authorization: Bearer $admin_token")
	[ "$code" = "200" ] && ok "admin KYC pending list reachable (code=$code)" || fail "admin KYC pending list got $code, expected 200"

	log "GET /admin/vendors/health (docs/plan/40 Task T5) on payin + payout — operator sees vendor state without SQL/log-diving..."
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$PAYIN_ADMIN_PORT/admin/payin/vendors/health" \
		-H "Authorization: Bearer $admin_token")
	[ "$code" = "200" ] && ok "admin payin vendor health reachable (code=$code)" || fail "admin payin vendor health got $code, expected 200"
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/health" \
		-H "Authorization: Bearer $admin_token")
	[ "$code" = "200" ] && ok "admin payout vendor health reachable (code=$code)" || fail "admin payout vendor health got $code, expected 200"
}

# ─── Section 8: request tracing end-to-end (docs/plan/36 Task T6) ───────────

trace_check() {
	log "=== 8. Request tracing end-to-end: one request_id, gateway to storage ==="

	local trace_id="e2e-trace-$RANDOM"
	local headers_file code
	headers_file="$(mktemp)"
	code=$(curl -s -o /dev/null -D "$headers_file" -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-H "X-Request-Id: $trace_id" \
		-d "{\"idempotency_key\":\"e2e-trace-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$USER_B\"}")
	[ "${code:0:1}" = "2" ] && ok "trace transfer posted (code=$code)" || fail "trace transfer got $code, expected 2xx"

	grep -qi "^X-Request-Id: $trace_id" "$headers_file" \
		&& ok "gateway response echoes X-Request-Id" \
		|| fail "gateway response did not echo X-Request-Id $trace_id"
	rm -f "$headers_file"

	grep -q "$trace_id" "$GATEWAY_LOG" \
		&& ok "gateway log contains request_id $trace_id" \
		|| fail "gateway log missing request_id $trace_id"
	grep -q "$trace_id" "$LEDGER_LOG" \
		&& ok "ledger-service log contains request_id $trace_id" \
		|| fail "ledger-service log missing request_id $trace_id"

	local stored
	stored="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT request_id FROM ledger_transactions WHERE idempotency_key = 'e2e-trace-$RUN_ID';")"
	[ "$stored" = "$trace_id" ] \
		&& ok "ledger_transactions.request_id matches ($trace_id)" \
		|| fail "stored request_id was '$stored', expected $trace_id"

	log "tracing the failover payout from section 6: payout_requests.request_id + CorrelationId reaching fraud's async velocity consumer..."
	local stored_payout_request_id
	stored_payout_request_id="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT request_id FROM payout_requests WHERE id = '$FAILOVER_PAYOUT_ID';")"
	[ "$stored_payout_request_id" = "$FAILOVER_REQUEST_ID" ] \
		&& ok "payout_requests.request_id matches ($FAILOVER_REQUEST_ID)" \
		|| fail "stored payout request_id was '$stored_payout_request_id', expected $FAILOVER_REQUEST_ID"

	# The payout's withdraw_settle only publishes ledger.transaction.posted.v1
	# once settled; fraud-service's velocity consumer then processes it off
	# the outbox relay -> RabbitMQ hop — asynchronous, so poll rather than a
	# single immediate grep (same rationale as await_notification).
	await_log_line "$FRAUD_LOG" "$FAILOVER_REQUEST_ID" \
		"fraud-service velocity consumer log carries the payout's CorrelationId ($FAILOVER_REQUEST_ID)"
}

# ─── Main ────────────────────────────────────────────────────────────────────

ensure_deps_up
build_server
start_services

onboard
topup
kyc_journey
transfer
withdraw
quote_journey
ops
trace_check

stop_services

echo
if [ "$FAILED" = "0" ]; then
	printf '\033[1;32m=== FULL BUSINESS JOURNEY PASSED — MVP end-user-to-daily-ops verified ===\033[0m\n'
	exit 0
else
	printf '\033[1;31m=== ONE OR MORE BUSINESS JOURNEY ASSERTIONS FAILED ===\033[0m\n'
	exit 1
fi
