#!/usr/bin/env bash
# Local Kubernetes business E2E. Run after bootstrap-local.sh (or use the
# make k8s-e2e target): it reaches the application only through the Traefik
# Gateway and proves register/login/KYC, signed callback settlement, and a
# transfer between two users.
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
PORT="${PORT:-8443}"
TMP_ROOT="${TMPDIR:-/tmp}"
TMP_DIR="$(mktemp -d "$TMP_ROOT/seev-k8s-e2e.XXXXXX")"
PF_PID=""
LAST_CODE=""
LAST_BODY=""
trap 'if [ -n "$PF_PID" ]; then kill "$PF_PID" >/dev/null 2>&1 || true; wait "$PF_PID" >/dev/null 2>&1 || true; fi; rm -rf "$TMP_DIR"' EXIT

command -v "$KUBECTL_BIN" >/dev/null 2>&1 || { printf 'k8s-e2e: kubectl is required\n' >&2; exit 2; }
command -v curl >/dev/null 2>&1 || { printf 'k8s-e2e: curl is required\n' >&2; exit 2; }
command -v openssl >/dev/null 2>&1 || { printf 'k8s-e2e: openssl is required\n' >&2; exit 2; }

json_field() {
	sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

api_request() {
	local method=$1 path=$2 body=$3 token=$4
	local response_file="$TMP_DIR/response"
	LAST_CODE="$(curl --noproxy '*' -ksS -o "$response_file" -w '%{http_code}' \
		--resolve "api.local.seev.test:$PORT:127.0.0.1" \
		-X "$method" "https://api.local.seev.test:$PORT$path" \
		-H 'Content-Type: application/json' \
		-H "Authorization: Bearer $token" \
		-d "$body")"
	LAST_BODY="$(<"$response_file")"
}

expect_success() {
	local label=$1
	case "$LAST_CODE" in
		2*) printf 'k8s-e2e: %s passed (HTTP %s)\n' "$label" "$LAST_CODE" ;;
		*) printf 'k8s-e2e: %s failed (HTTP %s): %s\n' "$label" "$LAST_CODE" "$LAST_BODY" >&2; exit 1 ;;
	esac
}

"$KUBECTL_BIN" wait -n seev-edge --for=condition=Available deployment/traefik --timeout=180s >/dev/null
"$KUBECTL_BIN" port-forward -n seev-edge service/traefik "$PORT":443 >"$TMP_DIR/port-forward.log" 2>&1 &
PF_PID=$!
for _ in $(seq 1 30); do
	curl --noproxy '*' -ksS --resolve "api.local.seev.test:$PORT:127.0.0.1" \
		"https://api.local.seev.test:$PORT/api/v1/auth/login" >/dev/null 2>&1 && break
	sleep 1
done

EMAIL="k8s-e2e-$(date +%s)-$$@example.com"
PASSWORD="K8sE2E!2026"
api_request POST /api/v1/auth/register "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"full_name\":\"Kubernetes E2E\"}" ""
expect_success "user registration through Gateway"
USER_ID="$(printf '%s' "$LAST_BODY" | json_field id)"
[ -n "$USER_ID" ] || { printf 'k8s-e2e: registration did not return a user id\n' >&2; exit 1; }

RECIPIENT_EMAIL="k8s-e2e-recipient-$(date +%s)-$$@example.com"
api_request POST /api/v1/auth/register "{\"email\":\"$RECIPIENT_EMAIL\",\"password\":\"$PASSWORD\",\"full_name\":\"Kubernetes E2E Recipient\"}" ""
expect_success "recipient registration through Gateway"
RECIPIENT_ID="$(printf '%s' "$LAST_BODY" | json_field id)"
[ -n "$RECIPIENT_ID" ] || { printf 'k8s-e2e: recipient registration did not return a user id\n' >&2; exit 1; }

api_request POST /api/v1/auth/login "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" ""
expect_success "user login through Gateway"
ACCESS_TOKEN="$(printf '%s' "$LAST_BODY" | json_field access_token)"
REFRESH_TOKEN="$(printf '%s' "$LAST_BODY" | json_field refresh_token)"
[ -n "$ACCESS_TOKEN" ] || { printf 'k8s-e2e: login did not return an access token\n' >&2; exit 1; }
[ -n "$REFRESH_TOKEN" ] || { printf 'k8s-e2e: login did not return a refresh token\n' >&2; exit 1; }

api_request POST /api/v1/users/me/kyc '{"level_requested":1}' "$ACCESS_TOKEN"
expect_success "KYC request through Gateway"
sleep 1
api_request POST /api/v1/auth/refresh "{\"refresh_token\":\"$REFRESH_TOKEN\"}" ""
if [ "$LAST_CODE" != "200" ]; then
	api_request POST /api/v1/auth/login "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" ""
	expect_success "login after KYC approval"
fi
ACCESS_TOKEN="$(printf '%s' "$LAST_BODY" | json_field access_token)"
[ -n "$ACCESS_TOKEN" ] || { printf 'k8s-e2e: refreshed/login response did not return an access token\n' >&2; exit 1; }

api_request POST /api/v1/topup '{"amount":"500000"}' "$ACCESS_TOKEN"
expect_success "top-up intent through Gateway"
INTENT_ID="$(printf '%s' "$LAST_BODY" | json_field id)"
REFERENCE="$(printf '%s' "$LAST_BODY" | json_field reference)"
[ -n "$INTENT_ID" ] && [ -n "$REFERENCE" ] || { printf 'k8s-e2e: top-up response omitted id/reference\n' >&2; exit 1; }

MOCKVENDOR_SECRET="$("$KUBECTL_BIN" get secret seev-runtime-secrets -n seev-app -o jsonpath='{.data.vendor-mockvendor-secret}' | base64 --decode)"
BODY="{\"event_id\":\"k8s-e2e-$(date +%s)-$$\",\"external_ref\":\"$REFERENCE\",\"user_id\":\"$USER_ID\",\"amount\":\"500000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
SIGNATURE="$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$MOCKVENDOR_SECRET" -r | awk '{print $1}')"
CALLBACK_CODE="$(curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}' \
	--resolve "callback.local.seev.test:$PORT:127.0.0.1" \
	-X POST "https://callback.local.seev.test:$PORT/webhooks/mockvendor" \
	-H "X-Mock-Signature: $SIGNATURE" -H 'Content-Type: application/json' -d "$BODY")"
case "$CALLBACK_CODE" in
	2*) printf 'k8s-e2e: signed callback accepted (HTTP %s)\n' "$CALLBACK_CODE" ;;
	*) printf 'k8s-e2e: signed callback failed (HTTP %s)\n' "$CALLBACK_CODE" >&2; exit 1 ;;
esac

for _ in $(seq 1 30); do
	api_request GET "/api/v1/topup/$INTENT_ID" "" "$ACCESS_TOKEN"
	[ "$(printf '%s' "$LAST_BODY" | json_field status)" = "settled" ] && break
	sleep 1
done
[ "$(printf '%s' "$LAST_BODY" | json_field status)" = "settled" ] || {
	printf 'k8s-e2e: top-up did not settle: %s\n' "$LAST_BODY" >&2
	exit 1
}
printf 'k8s-e2e: top-up settled through Kubernetes edge and callback path\n'

api_request POST /api/v1/ledger/transactions \
	"{\"idempotency_key\":\"k8s-e2e-transfer-$(date +%s)-$$\",\"type\":\"transfer_p2p\",\"amount\":\"1000\",\"currency\":\"IDR\",\"target_user_id\":\"$RECIPIENT_ID\"}" \
	"$ACCESS_TOKEN"
expect_success "ledger transfer through Gateway"
printf 'k8s-e2e: register/login/KYC/callback/top-up/transfer business journey passed\n'
