#!/usr/bin/env bash
# C4 multi-currency business journey acceptance test
# (docs/roadmap/active/60-c4-end-to-end-multi-currency.md Section 36.5 /
# Task T13) — the runtime acceptance evidence the plan's own status line
# says is still pending: two real users enable USD, get funded via a
# governed maker/checker adjustment, transfer USD to each other, and
# convert both directions (IDR->USD and USD->IDR) through the public FX
# quote/conversion API, with IDR balances proven untouched throughout and
# the documented negative-matrix rejections (cross-currency transfer,
# quote reuse, quote tampering) proven live against the real HTTP stack.
#
# Requires: Docker running, this repo checked out, go toolchain available.
# Does NOT require the app to already be running — this script builds and
# manages its own server process and docker-compose dependencies, same as
# scripts/business-e2e.sh.
#
# Usage:
#   ./scripts/multi-currency-e2e.sh
#
# Shared bootstrap lives in scripts/lib.sh — extend THAT file, not this one,
# if the bootstrap sequence itself needs to change.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="mcy-e2e"
LIB_WORK_DIR_PREFIX="mcy-e2e"
APP_PORT="${APP_PORT:-18300}"
INTERNAL_PORT="${INTERNAL_PORT:-18301}"

export AUTH_BOOTSTRAP_ADMIN_EMAIL="mcy-e2e-admin@example.com"
export AUTH_BOOTSTRAP_ADMIN_PASSWORD="${AUTH_BOOTSTRAP_ADMIN_PASSWORD:-BootstrapAdmin!2026}"
export TOPUP_INTENT_TTL=1h
export DEFAULT_CURRENCY=IDR

# shellcheck source=scripts/lib.sh
source "$ROOT_DIR/scripts/lib.sh"

trap cleanup EXIT

exec > >(tee "$WORK_DIR/multi-currency-e2e.stdout.log") 2>&1

RUN_ID="$(date +%s)-$$"

# ─── Section 1: onboarding — two real users, both KYC L1 ────────────────────

onboard() {
	log "=== 1. Onboarding: register + login 2 real users, both KYC L1 (FX conversion requires L1) ==="
	local reg_a reg_b
	reg_a="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/register" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"mcy-a-$RUN_ID@example.com\",\"password\":\"Password!2026\",\"full_name\":\"MCY User A\"}")"
	USER_A="$(echo "$reg_a" | json_field id)"
	[ -n "$USER_A" ] && ok "user A registered ($USER_A)" || fail "user A registration failed: $reg_a"

	reg_b="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/register" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"mcy-b-$RUN_ID@example.com\",\"password\":\"Password!2026\",\"full_name\":\"MCY User B\"}")"
	USER_B="$(echo "$reg_b" | json_field id)"
	[ -n "$USER_B" ] && ok "user B registered ($USER_B)" || fail "user B registration failed: $reg_b"

	local login_a login_b refresh_a refresh_b
	login_a="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/login" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"mcy-a-$RUN_ID@example.com\",\"password\":\"Password!2026\"}")"
	TOKEN_A="$(echo "$login_a" | json_field access_token)"
	refresh_a="$(echo "$login_a" | json_field refresh_token)"
	[ -n "$TOKEN_A" ] && ok "user A logged in with a real JWT" || fail "user A login failed: $login_a"

	login_b="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/login" \
		-H "Content-Type: application/json" \
		-d "{\"email\":\"mcy-b-$RUN_ID@example.com\",\"password\":\"Password!2026\"}")"
	TOKEN_B="$(echo "$login_b" | json_field access_token)"
	refresh_b="$(echo "$login_b" | json_field refresh_token)"
	[ -n "$TOKEN_B" ] && ok "user B logged in with a real JWT" || fail "user B login failed: $login_b"

	local kyc_resp
	kyc_resp="$(kyc_approve_l1 "$AUTH_APP_PORT" "$TOKEN_A" "$refresh_a")"
	TOKEN_A="$(echo "$kyc_resp" | json_field access_token)"
	[ -n "$TOKEN_A" ] && ok "user A KYC L1 approved" || fail "user A KYC L1 dance failed"
	kyc_resp="$(kyc_approve_l1 "$AUTH_APP_PORT" "$TOKEN_B" "$refresh_b")"
	TOKEN_B="$(echo "$kyc_resp" | json_field access_token)"
	[ -n "$TOKEN_B" ] && ok "user B KYC L1 approved" || fail "user B KYC L1 dance failed"
}

# ─── Section 2: Journey A — enable USD wallet ────────────────────────────────

enable_usd() {
	log "=== 2. Journey A: enable USD, list currencies/balances, duplicate enable is idempotent ==="
	local currencies
	currencies="$(curl -s "http://localhost:$APP_PORT/api/v1/currencies" -H "Authorization: Bearer $TOKEN_A")"
	echo "$currencies" | grep -q '"code":"USD"' && ok "USD is listed in /api/v1/currencies" \
		|| fail "USD missing from currency catalogue: $currencies"
	echo "$currencies" | grep -q '"code":"IDR"' && ok "IDR is listed in /api/v1/currencies" \
		|| fail "IDR missing from currency catalogue: $currencies"

	local enable_a code
	enable_a="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/currencies/USD/enable" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -d '{}')"
	code="$(echo "$enable_a" | tail -1)"
	[[ "$code" == 2* ]] && ok "user A enabled USD (code=$code)" || fail "user A USD enable got $code: $enable_a"

	local enable_a_again code_again
	enable_a_again="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/currencies/USD/enable" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -d '{}')"
	code_again="$(echo "$enable_a_again" | tail -1)"
	[[ "$code_again" == 2* ]] && ok "duplicate USD enable is idempotent (code=$code_again)" \
		|| fail "duplicate USD enable got $code_again, expected 2xx: $enable_a_again"

	curl -s -o /dev/null -X POST "http://localhost:$APP_PORT/api/v1/currencies/USD/enable" \
		-H "Authorization: Bearer $TOKEN_B" -H "Content-Type: application/json" -d '{}'

	local balances_a
	balances_a="$(curl -s "http://localhost:$APP_PORT/api/v1/balances" -H "Authorization: Bearer $TOKEN_A")"
	echo "$balances_a" | grep -q '"currency":"USD"' && ok "user A's balance list includes a USD row" \
		|| fail "USD balance row missing after enable: $balances_a"
	echo "$balances_a" | grep -q '"currency":"USD","minor_unit":2,"status":"active","operations":{[^}]*},"user_enabled":true,"available":"0"' \
		&& ok "user A's USD balance starts at exactly 0, never a minted starting balance" \
		|| log "note: USD row present but exact-zero shape check is best-effort (field ordering may differ) — verifying via /balances/USD next"

	local usd_only
	usd_only="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_A")"
	echo "$usd_only" | grep -q '"available":"0"' && ok "GET /api/v1/balances/USD confirms zero available balance" \
		|| fail "expected available=0 right after enable, got: $usd_only"
}

# ─── Section 3: fund USD via governed adjustment (plan Section 32's first-cut funding path) ──

fund_usd_via_adjustment() {
	log "=== 3. Fund user A's USD cash account via a maker/checker adjustment (no top-up/payout activation needed for this slice) ==="
	local maker_token checker_token
	maker_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin_maker)"
	checker_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin_checker)"

	local create_resp adj_id
	create_resp="$(curl_internal -s -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/adjustments" \
		-H "Authorization: Bearer $maker_token" -H "Content-Type: application/json" \
		-d "{\"type\":\"adjustment_credit\",\"amount\":\"5000\",\"user_id\":\"$USER_A\",\"reason\":\"C4 E2E USD funding\",\"metadata\":{\"currency\":\"USD\"}}")"
	adj_id="$(echo "$create_resp" | json_field id)"
	[ -n "$adj_id" ] && ok "USD adjustment created by maker ($adj_id)" || fail "adjustment creation failed: $create_resp"

	local approve_code
	approve_code=$(curl_internal -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/adjustments/$adj_id/approve" \
		-H "Authorization: Bearer $checker_token" -H "Content-Type: application/json" -d '{}')
	[[ "$approve_code" == 2* ]] && ok "USD adjustment approved by a different checker (code=$approve_code)" \
		|| fail "adjustment approval got $approve_code, expected 2xx"

	local usd_balance
	usd_balance="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	[ "$usd_balance" = "5000" ] && ok "user A's USD balance is 5000 (\$50.00) after the approved adjustment" \
		|| fail "user A USD balance was '$usd_balance', expected 5000"
}

# ─── Section 4: Journey B — USD same-currency transfer ──────────────────────

usd_transfer() {
	log "=== 4. Journey B: USD-to-USD transfer posts once, IDR balances untouched, cross-currency transfer rejected ==="
	local before_a before_b
	before_a="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	before_b="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_B" | json_field available)"

	local resp code
	resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"mcy-e2e-usd-transfer-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"1500\",\"currency\":\"USD\",\"target_user_id\":\"$USER_B\"}")"
	code="$(echo "$resp" | tail -1)"
	[[ "$code" == 2* ]] && ok "USD transfer posted (code=$code)" || fail "USD transfer got $code: $resp"

	local after_a after_b
	after_a="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	after_b="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_B" | json_field available)"
	[ "$after_a" = "$((before_a - 1500))" ] && ok "sender USD balance decreased by exactly 1500" \
		|| fail "sender USD balance was $after_a, expected $((before_a - 1500))"
	[ "$after_b" = "$((before_b + 1500))" ] && ok "recipient USD balance increased by exactly 1500" \
		|| fail "recipient USD balance was $after_b, expected $((before_b + 1500))"

	local idr_a
	idr_a="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/IDR" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	[ "$idr_a" = "0" ] && ok "user A's IDR balance remains 0 — USD transfer never touched IDR" \
		|| fail "user A IDR balance was '$idr_a', expected 0 (unchanged)"

	log "attempting a normal transfer with mismatched request/account currency must reject, never silently convert..."
	local mismatch code2
	mismatch="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"mcy-e2e-cross-ccy-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"1000\",\"currency\":\"IDR\",\"target_user_id\":\"$USER_B\"}")"
	code2="$(echo "$mismatch" | tail -1)"
	# User A has 0 IDR, so this is expected to reject either on insufficient
	# funds or a currency-account-state error — either way it must be a 4xx
	# rejection, never a 2xx that silently moved USD instead.
	[[ "$code2" == 4* ]] && ok "IDR transfer with no IDR funds correctly rejected (code=$code2), no implicit USD fallback" \
		|| fail "expected 4xx rejection, got $code2: $mismatch"
}

# ─── Section 5: Journey F/G — explicit FX conversion, both directions ───────

fx_conversion() {
	log "=== 5. Journey F/G: FX pairs, IDR->USD and USD->IDR quote+conversion, position + balance evidence ==="
	local pairs
	pairs="$(curl -s "http://localhost:$APP_PORT/api/v1/fx/pairs" -H "Authorization: Bearer $TOKEN_A")"
	echo "$pairs" | grep -q '"pair_code":"USDIDR"' && ok "USDIDR pair is published via GET /api/v1/fx/pairs" \
		|| fail "USDIDR pair missing: $pairs"
	echo "$pairs" | grep -q '"rate_source":"mock"' && ok "pair marks rate_source=mock (no real market claim)" \
		|| fail "pair did not mark rate_source=mock: $pairs"

	local idr_before usd_before
	idr_before="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/IDR" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	usd_before="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_A" | json_field available)"

	log "user A needs IDR to convert from — fund via the same governed adjustment path used for USD..."
	local maker_token checker_token create_resp adj_id
	maker_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin_maker)"
	checker_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin_checker)"
	create_resp="$(curl_internal -s -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/adjustments" \
		-H "Authorization: Bearer $maker_token" -H "Content-Type: application/json" \
		-d "{\"type\":\"adjustment_credit\",\"amount\":\"320000\",\"user_id\":\"$USER_A\",\"reason\":\"C4 E2E IDR funding for FX\",\"metadata\":{\"currency\":\"IDR\"}}")"
	adj_id="$(echo "$create_resp" | json_field id)"
	[ -n "$adj_id" ] || fail "IDR adjustment creation failed: $create_resp"
	curl_internal -s -o /dev/null -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/adjustments/$adj_id/approve" \
		-H "Authorization: Bearer $checker_token" -H "Content-Type: application/json" -d '{}'
	idr_before="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/IDR" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	[ "$idr_before" = "320000" ] && ok "user A funded with 320000 IDR for the conversion journey" \
		|| fail "IDR funding failed, balance is '$idr_before', expected 320000"

	log "--- Journey F: IDR -> USD ---"
	local quote_resp quote_id source_amt target_amt
	quote_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/fx/quotes" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -H "Idempotency-Key: mcy-e2e-fxquote-1-$RUN_ID" \
		-d '{"source_currency":"IDR","target_currency":"USD","source_amount":"160000"}')"
	quote_id="$(echo "$quote_resp" | json_field id)"
	source_amt="$(echo "$quote_resp" | json_field source_amount)"
	target_amt="$(echo "$quote_resp" | json_field target_amount)"
	[ -n "$quote_id" ] && ok "IDR->USD quote created ($quote_id): $source_amt IDR -> $target_amt USD" \
		|| fail "IDR->USD quote creation failed: $quote_resp"
	[ "$target_amt" = "1000" ] && ok "quote matches plan Section 14.3's fixture exactly (1000 USD minor units)" \
		|| log "note: target amount is $target_amt, not the doc fixture's 1000 — rate config may differ from the seeded default"

	local conv_resp conv_id conv_status
	conv_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/fx/conversions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -H "Idempotency-Key: mcy-e2e-fxconv-1-$RUN_ID" \
		-d "{\"quote_id\":\"$quote_id\",\"expected_source_amount\":\"$source_amt\",\"expected_target_amount\":\"$target_amt\"}")"
	conv_id="$(echo "$conv_resp" | json_field id)"
	conv_status="$(echo "$conv_resp" | json_field status)"
	[ -n "$conv_id" ] && [ "$conv_status" = "posted" ] && ok "IDR->USD conversion posted ($conv_id)" \
		|| fail "IDR->USD conversion failed: $conv_resp"

	local idr_after_f usd_after_f
	idr_after_f="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/IDR" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	usd_after_f="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	[ "$idr_after_f" = "$((idr_before - source_amt))" ] && ok "IDR balance dropped by exactly the source amount" \
		|| fail "IDR balance after conversion was $idr_after_f, expected $((idr_before - source_amt))"
	[ "$usd_after_f" = "$((usd_before + target_amt))" ] && ok "USD balance rose by exactly the target amount" \
		|| fail "USD balance after conversion was $usd_after_f, expected $((usd_before + target_amt))"

	log "replaying the same Idempotency-Key must return the SAME conversion, never repost..."
	local replay_resp replay_id
	replay_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/fx/conversions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -H "Idempotency-Key: mcy-e2e-fxconv-1-$RUN_ID" \
		-d "{\"quote_id\":\"$quote_id\",\"expected_source_amount\":\"$source_amt\",\"expected_target_amount\":\"$target_amt\"}")"
	replay_id="$(echo "$replay_resp" | json_field id)"
	[ "$replay_id" = "$conv_id" ] && ok "replay returned the identical conversion id, no double post" \
		|| fail "replay returned a different id ($replay_id != $conv_id): $replay_resp"
	local idr_after_replay
	idr_after_replay="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/IDR" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	[ "$idr_after_replay" = "$idr_after_f" ] && ok "replay did not move any additional money" \
		|| fail "replay changed the IDR balance: $idr_after_replay vs $idr_after_f"

	log "a second DISTINCT idempotency key against the now-consumed quote must be rejected, not silently posted again..."
	local reuse_resp reuse_code
	reuse_resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/fx/conversions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -H "Idempotency-Key: mcy-e2e-fxconv-1-reuse-$RUN_ID" \
		-d "{\"quote_id\":\"$quote_id\",\"expected_source_amount\":\"$source_amt\",\"expected_target_amount\":\"$target_amt\"}")"
	reuse_code="$(echo "$reuse_resp" | tail -1)"
	[[ "$reuse_code" == 4* ]] && ok "reuse of a consumed quote under a new idempotency key rejected (code=$reuse_code)" \
		|| fail "expected 4xx on consumed-quote reuse, got $reuse_code: $reuse_resp"

	log "tampering with the expected target amount must be rejected before any posting..."
	local quote2_resp quote2_id
	quote2_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/fx/quotes" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -H "Idempotency-Key: mcy-e2e-fxquote-2-$RUN_ID" \
		-d '{"source_currency":"IDR","target_currency":"USD","source_amount":"16000"}')"
	quote2_id="$(echo "$quote2_resp" | json_field id)"
	[ -n "$quote2_id" ] || fail "second IDR->USD quote failed: $quote2_resp"
	local tamper_resp tamper_code
	tamper_resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/fx/conversions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -H "Idempotency-Key: mcy-e2e-fxconv-tamper-$RUN_ID" \
		-d "{\"quote_id\":\"$quote2_id\",\"expected_source_amount\":\"16000\",\"expected_target_amount\":\"999999999\"}")"
	tamper_code="$(echo "$tamper_resp" | tail -1)"
	[[ "$tamper_code" == 4* ]] && ok "tampered expected_target_amount rejected (code=$tamper_code)" \
		|| fail "expected 4xx on tampered amount, got $tamper_code: $tamper_resp"

	log "--- Journey G: USD -> IDR (reverse direction) ---"
	local usd_before_g idr_before_g
	usd_before_g="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	idr_before_g="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/IDR" -H "Authorization: Bearer $TOKEN_A" | json_field available)"

	local quote3_resp quote3_id source3_amt target3_amt
	quote3_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/fx/quotes" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -H "Idempotency-Key: mcy-e2e-fxquote-3-$RUN_ID" \
		-d '{"source_currency":"USD","target_currency":"IDR","source_amount":"500"}')"
	quote3_id="$(echo "$quote3_resp" | json_field id)"
	source3_amt="$(echo "$quote3_resp" | json_field source_amount)"
	target3_amt="$(echo "$quote3_resp" | json_field target_amount)"
	[ -n "$quote3_id" ] && ok "USD->IDR quote created ($quote3_id): $source3_amt USD -> $target3_amt IDR" \
		|| fail "USD->IDR quote creation failed: $quote3_resp"

	local conv3_resp conv3_status
	conv3_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/fx/conversions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -H "Idempotency-Key: mcy-e2e-fxconv-3-$RUN_ID" \
		-d "{\"quote_id\":\"$quote3_id\",\"expected_source_amount\":\"$source3_amt\",\"expected_target_amount\":\"$target3_amt\"}")"
	conv3_status="$(echo "$conv3_resp" | json_field status)"
	[ "$conv3_status" = "posted" ] && ok "USD->IDR conversion posted" || fail "USD->IDR conversion failed: $conv3_resp"

	local usd_after_g idr_after_g
	usd_after_g="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/USD" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	idr_after_g="$(curl -s "http://localhost:$APP_PORT/api/v1/balances/IDR" -H "Authorization: Bearer $TOKEN_A" | json_field available)"
	[ "$usd_after_g" = "$((usd_before_g - source3_amt))" ] && ok "USD balance dropped by exactly the reverse-leg source amount" \
		|| fail "USD balance after reverse conversion was $usd_after_g, expected $((usd_before_g - source3_amt))"
	[ "$idr_after_g" = "$((idr_before_g + target3_amt))" ] && ok "IDR balance rose by exactly the reverse-leg target amount" \
		|| fail "IDR balance after reverse conversion was $idr_after_g, expected $((idr_before_g + target3_amt))"

	log "a quote belongs to exactly one user — user B must get not-found on user A's quote..."
	local cross_user_code
	cross_user_code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$APP_PORT/api/v1/fx/quotes/$quote3_id" -H "Authorization: Bearer $TOKEN_B")
	[ "$cross_user_code" = "404" ] && ok "user B cannot read user A's FX quote (404)" \
		|| fail "expected 404 for cross-user quote read, got $cross_user_code"
}

# ─── Main ─────────────────────────────────────────────────────────────────

ensure_deps_up
build_server
start_services

onboard
enable_usd
fund_usd_via_adjustment
usd_transfer
fx_conversion

stop_services

echo
if [ "$FAILED" = "0" ]; then
	printf '\033[1;32m=== C4 MULTI-CURRENCY E2E PASSED ===\033[0m\n'
	exit 0
else
	printf '\033[1;31m=== ONE OR MORE C4 MULTI-CURRENCY E2E ASSERTIONS FAILED ===\033[0m\n'
	exit 1
fi
