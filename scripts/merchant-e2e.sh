#!/usr/bin/env bash
# End-to-end merchant/B2B journey (docs/roadmap/active/57-c1-merchant-b2b-api.md
# T10): sandbox onboarding -> account/transaction reads -> a real payin
# settling through mockvendor's signed webhook -> a merchant-to-merchant
# transfer -> idempotency replay (no double-debit) -> cross-tenant
# isolation -> the T9 global kill switch. Every merchant-facing call goes
# over real HTTP through the actual assembled Gateway process, exactly as
# a real merchant client would use it — no in-process shortcuts.
#
# Requires: Docker running, this repo checked out, go toolchain available,
# openssl (HMAC-signing the mockvendor webhook, same as smoke-test.sh/
# business-e2e.sh's own topup journey).
# Does NOT require the app to already be running — this script builds and
# manages its own server processes and docker-compose dependencies.
#
# Usage:
#   ./scripts/merchant-e2e.sh
#
# Shared bootstrap lives in scripts/lib.sh — extend THAT file, not this
# one, if the bootstrap sequence itself needs to change.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="merchant-e2e"
LIB_WORK_DIR_PREFIX="merchant-e2e"
source "$ROOT_DIR/scripts/lib.sh"
trap cleanup EXIT

RUN_ID="${RUN_ID:-$(date +%s)}"

ensure_deps_up
build_server
start_services

ADMIN_TOKEN="$(gen_token "merchant-e2e-operator-$RUN_ID" admin)"

# ─── Helpers ────────────────────────────────────────────────────────────────

# admin_post/admin_get hit Gateway's internal listener directly (mTLS +
# JWT `authed` chain — services/gateway/internal/transport/http.NewInternalRouter, Plan 57 T8),
# the same surface Admin BFF's own generic proxy targets.
admin_post() {
	local path=$1 body=$2
	curl_internal -sS -X POST "http://localhost:$INTERNAL_PORT/api/v1/admin/gateway$path" \
		-H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" -d "$body"
}
admin_put() {
	local path=$1 body=$2
	curl_internal -sS -X PUT "http://localhost:$INTERNAL_PORT/api/v1/admin/gateway$path" \
		-H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" -d "$body"
}
admin_get() {
	local path=$1
	curl_internal -sS "http://localhost:$INTERNAL_PORT/api/v1/admin/gateway$path" -H "Authorization: Bearer $ADMIN_TOKEN"
}

# b2b_get/b2b_post hit the PUBLIC merchant-facing surface with a real
# merchant API key — no mTLS client cert, exactly what an external
# merchant integration presents.
b2b_get() {
	local path=$1 key=$2
	curl -sS "http://localhost:$APP_PORT/api/v1/b2b$path" -H "Authorization: Bearer $key"
}
b2b_post() {
	local path=$1 key=$2 idem=$3 body=$4
	curl -sS -X POST "http://localhost:$APP_PORT/api/v1/b2b$path" \
		-H "Authorization: Bearer $key" -H "Idempotency-Key: $idem" -H "Content-Type: application/json" -d "$body"
}
b2b_post_code() {
	local path=$1 key=$2 idem=$3 body=$4
	curl -sS -o "$WORK_DIR/last_b2b_response.json" -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/b2b$path" \
		-H "Authorization: Bearer $key" -H "Idempotency-Key: $idem" -H "Content-Type: application/json" -d "$body"
}

# nested_field extracts a field from the {"success":true,"data":{...}}
# envelope every response uses — json_field's own plain sed match works
# fine on it regardless of nesting depth, same convention business-e2e.sh
# already relies on for payin/payout responses.
nested_field() { json_field "$1"; }

# ─── 1. Sandbox onboarding (Journey A) ───────────────────────────────────────

log "=== 1. Sandbox onboarding: create tenant -> provision account -> create key ==="

tenant_json="$(admin_post "/tenants" "{\"external_code\":\"e2e-a-$RUN_ID\",\"name\":\"E2E Merchant A\",\"environment\":\"sandbox\",\"default_currency\":\"IDR\"}")"
TENANT_A="$(echo "$tenant_json" | nested_field id)"
tenant_a_status="$(echo "$tenant_json" | nested_field status)"
[ -n "$TENANT_A" ] && [ "$tenant_a_status" = "active" ] && ok "sandbox tenant A created and immediately active ($TENANT_A)" \
	|| fail "sandbox tenant A creation failed: $tenant_json"

account_json="$(admin_post "/tenants/$TENANT_A/account" '{"currency":"IDR"}')"
echo "$account_json" | grep -q '"account_id"' && ok "tenant A's ledger cash account provisioned" || fail "account provisioning failed: $account_json"

key_json="$(admin_post "/tenants/$TENANT_A/keys" '{"environment":"sandbox","scopes":["merchant:read","accounts:read","transactions:read","transfers:write","payins:write","payins:read"]}')"
KEY_A="$(echo "$key_json" | nested_field plaintext)"
[ -n "$KEY_A" ] && ok "tenant A's API key created (plaintext shown once)" || fail "key creation failed: $key_json"

tenant_json_b="$(admin_post "/tenants" "{\"external_code\":\"e2e-b-$RUN_ID\",\"name\":\"E2E Merchant B\",\"environment\":\"sandbox\",\"default_currency\":\"IDR\"}")"
TENANT_B="$(echo "$tenant_json_b" | nested_field id)"
[ -n "$TENANT_B" ] && ok "sandbox tenant B created ($TENANT_B)" || fail "sandbox tenant B creation failed: $tenant_json_b"
admin_post "/tenants/$TENANT_B/account" '{"currency":"IDR"}' >/dev/null
key_json_b="$(admin_post "/tenants/$TENANT_B/keys" '{"environment":"sandbox","scopes":["merchant:read","accounts:read","transactions:read","transfers:write","payins:write","payins:read"]}')"
KEY_B="$(echo "$key_json_b" | nested_field plaintext)"
[ -n "$KEY_B" ] && ok "tenant B's API key created" || fail "tenant B key creation failed: $key_json_b"

# ─── 2. Merchant profile and account reads ──────────────────────────────────

log "=== 2. Merchant profile, accounts, transactions (all reads, zero balance) ==="

profile_json="$(b2b_get "/merchant" "$KEY_A")"
echo "$profile_json" | grep -q '"environment":"sandbox"' && ok "GET /merchant returns tenant A's own profile" \
	|| fail "GET /merchant unexpected response: $profile_json"

accounts_json="$(b2b_get "/accounts" "$KEY_A")"
ACCOUNT_A="$(echo "$accounts_json" | nested_field id)"
[ -n "$ACCOUNT_A" ] && ok "GET /accounts returns tenant A's single cash account ($ACCOUNT_A)" \
	|| fail "GET /accounts unexpected response: $accounts_json"

balance_json="$(b2b_get "/accounts/$ACCOUNT_A/balance" "$KEY_A")"
echo "$balance_json" | grep -q '"balance":"0"' && ok "fresh account balance is 0 before any money movement" \
	|| fail "unexpected initial balance: $balance_json"

accounts_json_b="$(b2b_get "/accounts" "$KEY_B")"
ACCOUNT_B="$(echo "$accounts_json_b" | nested_field id)"
[ -n "$ACCOUNT_B" ] && [ "$ACCOUNT_B" != "$ACCOUNT_A" ] && ok "tenant B has its own distinct account ($ACCOUNT_B)" \
	|| fail "tenant B account lookup failed or collided with tenant A: $accounts_json_b"

txns_json="$(b2b_get "/transactions" "$KEY_A")"
echo "$txns_json" | grep -q '"data":\[\]' && ok "tenant A's transaction list is empty before any money movement" \
	|| fail "expected empty transaction list, got: $txns_json"

# ─── 3. Merchant pay-in settling through mockvendor's signed webhook ────────

log "=== 3. Merchant pay-in: create (sandbox -> mockvendor) -> signed webhook -> settled ==="

payin_json="$(b2b_post "/payins" "$KEY_A" "e2e-payin-$RUN_ID" '{"amount":"100000","currency":"IDR"}')"
PAYIN_ID="$(echo "$payin_json" | nested_field id)"
payin_status="$(echo "$payin_json" | nested_field status)"
payin_vendor="$(echo "$payin_json" | nested_field vendor)"
[ -n "$PAYIN_ID" ] && [ "$payin_status" = "pending" ] && [ "$payin_vendor" = "mockvendor" ] \
	&& ok "merchant payin created, pending, routed to mockvendor (sandbox-to-mock, §T6) ($PAYIN_ID)" \
	|| fail "merchant payin creation failed: $payin_json"

# The merchant topup intent's Reference is what the vendor webhook's
# external_ref must match — same normalization VendorService already
# applies to the user-facing topup flow (scripts/business-e2e.sh's own
# topup()), the merchant path shares the identical webhook ingestion.
reference="$(psql_exec "$PAYIN_DB_NAME" -tA -c "SELECT reference FROM payin_topup_intents WHERE id = '$PAYIN_ID';" | tr -d '[:space:]')"
[ -n "$reference" ] && ok "resolved merchant payin's own reference for the settlement webhook ($reference)" \
	|| fail "could not resolve payin_topup_intents.reference for id=$PAYIN_ID"

webhook_body="{\"event_id\":\"e2e-merchant-payin-$RUN_ID\",\"external_ref\":\"$reference\",\"user_id\":\"$(uuidgen | tr '[:upper:]' '[:lower:]')\",\"amount\":\"100000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
sig="$(printf '%s' "$webhook_body" | openssl dgst -sha256 -hmac "$VENDOR_MOCKVENDOR_SECRET" -r | awk '{print $1}')"
webhook_code="$(curl_internal -sS -o /dev/null -w '%{http_code}' -X POST "http://localhost:$VENDOR_APP_PORT/webhooks/mockvendor" \
	-H "X-Mock-Signature: $sig" -H "Content-Type: application/json" -d "$webhook_body")"
[ "${webhook_code:0:1}" = "2" ] && ok "signed mockvendor webhook accepted (code=$webhook_code)" \
	|| fail "mockvendor webhook got $webhook_code, expected 2xx"

payin_status_after="$(b2b_get "/payins/$PAYIN_ID" "$KEY_A" | nested_field status)"
[ "$payin_status_after" = "settled" ] && ok "merchant payin status is 'settled' after the webhook" \
	|| fail "payin status after webhook was '$payin_status_after', expected 'settled'"

balance_after_payin="$(b2b_get "/accounts/$ACCOUNT_A/balance" "$KEY_A" | nested_field balance)"
[ "$balance_after_payin" = "100000" ] && ok "tenant A's balance is 100000 after the settled payin" \
	|| fail "tenant A balance after payin was '$balance_after_payin', expected 100000"

# ─── 4. Merchant-to-merchant transfer + idempotent replay ───────────────────

log "=== 4. Merchant transfer A -> B, then replay (must not double-debit) ==="

transfer_body="{\"destination_account_id\":\"$ACCOUNT_B\",\"amount\":\"30000\",\"currency\":\"IDR\"}"
transfer_json="$(b2b_post "/transfers" "$KEY_A" "e2e-xfer-$RUN_ID" "$transfer_body")"
TRANSFER_ID="$(echo "$transfer_json" | nested_field id)"
transfer_status="$(echo "$transfer_json" | nested_field status)"
[ -n "$TRANSFER_ID" ] && [ "$transfer_status" = "posted" ] && ok "merchant transfer posted ($TRANSFER_ID)" \
	|| fail "merchant transfer creation failed: $transfer_json"

balance_a_after_xfer="$(b2b_get "/accounts/$ACCOUNT_A/balance" "$KEY_A" | nested_field balance)"
balance_b_after_xfer="$(b2b_get "/accounts/$ACCOUNT_B/balance" "$KEY_B" | nested_field balance)"
[ "$balance_a_after_xfer" = "70000" ] && ok "tenant A debited to 70000" || fail "tenant A balance after transfer was '$balance_a_after_xfer', expected 70000"
[ "$balance_b_after_xfer" = "30000" ] && ok "tenant B credited to 30000" || fail "tenant B balance after transfer was '$balance_b_after_xfer', expected 30000"

replay_code="$(b2b_post_code "/transfers" "$KEY_A" "e2e-xfer-$RUN_ID" "$transfer_body")"
replay_json="$(cat "$WORK_DIR/last_b2b_response.json")"
replay_id="$(echo "$replay_json" | nested_field id)"
[ "$replay_code" = "201" ] && [ "$replay_id" = "$TRANSFER_ID" ] && ok "replayed transfer (same idempotency key) returns the ORIGINAL transaction (code=$replay_code)" \
	|| fail "replay expected 201 with id=$TRANSFER_ID, got code=$replay_code body=$replay_json"

balance_a_after_replay="$(b2b_get "/accounts/$ACCOUNT_A/balance" "$KEY_A" | nested_field balance)"
[ "$balance_a_after_replay" = "70000" ] && ok "tenant A's balance UNCHANGED after replay — no double-debit" \
	|| fail "tenant A balance after replay was '$balance_a_after_replay', expected 70000 (double-debit bug)"

# ─── 5. Cross-tenant isolation ──────────────────────────────────────────────

log "=== 5. Cross-tenant isolation: B cannot read A's payin; A cannot read B's account as its own ==="

cross_code="$(curl -sS -o "$WORK_DIR/cross.json" -w '%{http_code}' "http://localhost:$APP_PORT/api/v1/b2b/payins/$PAYIN_ID" -H "Authorization: Bearer $KEY_B")"
[ "$cross_code" = "404" ] && ok "tenant B cannot read tenant A's payin (404, existence not leaked)" \
	|| fail "cross-tenant payin read returned $cross_code, expected 404"

cross_account_code="$(curl -sS -o "$WORK_DIR/cross2.json" -w '%{http_code}' "http://localhost:$APP_PORT/api/v1/b2b/accounts/$ACCOUNT_B" -H "Authorization: Bearer $KEY_A")"
[ "$cross_account_code" = "404" ] && ok "tenant A cannot address tenant B's real account id as its own (404)" \
	|| fail "cross-tenant account read returned $cross_account_code, expected 404"

both_see_json_a="$(b2b_get "/transactions/$TRANSFER_ID" "$KEY_A")"
both_see_json_b="$(b2b_get "/transactions/$TRANSFER_ID" "$KEY_B")"
echo "$both_see_json_a" | grep -q "\"id\":\"$TRANSFER_ID\"" && echo "$both_see_json_b" | grep -q "\"id\":\"$TRANSFER_ID\"" \
	&& ok "both legitimate parties to the transfer can read it via their own account" \
	|| fail "one of the two legitimate parties could not read the shared transfer"

# ─── 6. Global kill switch (T9) ──────────────────────────────────────────────

log "=== 6. Global B2B kill switch: disable -> B2B calls fail -> re-enable -> recovers ==="

flag_json="$(admin_get "/global/b2b-api")"
echo "$flag_json" | grep -q '"b2b_api_enabled":true' && ok "kill switch defaults to enabled" || fail "unexpected default kill-switch state: $flag_json"

admin_maker_token="$(gen_token "merchant-e2e-maker-$RUN_ID" admin_maker)"
disable_code="$(curl_internal -sS -o /dev/null -w '%{http_code}' -X PUT "http://localhost:$INTERNAL_PORT/api/v1/admin/gateway/global/b2b-api" \
	-H "Authorization: Bearer $admin_maker_token" -H "Content-Type: application/json" -d '{"enabled":false}')"
[ "$disable_code" = "403" ] && ok "maker role cannot disable the global kill switch (checker-only, §16.3)" \
	|| fail "maker disable attempt returned $disable_code, expected 403"

admin_put "/global/b2b-api" '{"enabled":false}' >/dev/null
disabled_code="$(curl -sS -o /dev/null -w '%{http_code}' "http://localhost:$APP_PORT/api/v1/b2b/merchant" -H "Authorization: Bearer $KEY_A")"
[ "$disabled_code" = "503" ] && ok "B2B calls fail with 503 while the kill switch is disabled" \
	|| fail "expected 503 while disabled, got $disabled_code"

admin_put "/global/b2b-api" '{"enabled":true}' >/dev/null
reenabled_code="$(curl -sS -o /dev/null -w '%{http_code}' "http://localhost:$APP_PORT/api/v1/b2b/merchant" -H "Authorization: Bearer $KEY_A")"
[ "$reenabled_code" = "200" ] && ok "B2B calls succeed again immediately after re-enabling" \
	|| fail "expected 200 after re-enabling, got $reenabled_code"

if [ "${FAILED:-0}" -ne 0 ]; then
	exit 1
fi
log "merchant-e2e completed"
