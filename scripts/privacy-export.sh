#!/usr/bin/env bash
# docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T4 (K9, work item 6): exercises the
# authenticated user export flow end to end against an ALREADY-RUNNING
# auth-service (this script does not manage the stack's lifecycle itself —
# unlike scripts/business-e2e.sh, it's an operator/local convenience tool,
# not a CI acceptance gate).
#
# Registers a throwaway user, requests an export (password re-verified),
# polls until ready, downloads it, and reports metadata ONLY — archive
# file listing, manifest schema_version/owners. Never cats a data file's
# own content (this task's own "without printing archive contents"
# requirement) — the downloaded bytes are decrypted server-side, so this
# script deliberately limits itself to `unzip -l` and the manifest's own
# summary fields.
#
# Usage:
#   AUTH_URL=http://localhost:8082 ./scripts/privacy-export.sh
#
# Requires: curl, unzip, jq (or python3 as a jq fallback), openssl (random
# throwaway email/password).
set -euo pipefail

AUTH_URL="${AUTH_URL:-http://localhost:8082}"
TLS_CERT_DIR="${TLS_CERT_DIR:-deploy/certs}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

log()  { printf '\033[1;34m[privacy-export]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[ pass]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[ FAIL]\033[0m %s\n' "$*"; exit 1; }

# App-profile HTTP listeners are mTLS-protected. Keep an explicit HTTP
# escape hatch for callers that intentionally run a non-mTLS local binary.
CURL_AUTH_ARGS=()
if [[ "$AUTH_URL" == https://* ]]; then
	CURL_AUTH_ARGS=(-k --cacert "$TLS_CERT_DIR/ca.pem" --cert "$TLS_CERT_DIR/dev-operator.pem" --key "$TLS_CERT_DIR/dev-operator-key.pem")
fi
curl_auth() {
	if [ "${#CURL_AUTH_ARGS[@]}" -gt 0 ]; then
		curl "${CURL_AUTH_ARGS[@]}" "$@"
	else
		curl "$@"
	fi
}

json_field() {
	if command -v jq >/dev/null 2>&1; then
		jq -r ".$1 // empty"
	else
		python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1',''))"
	fi
}

EMAIL="privacy-export-$(date +%s)-$RANDOM@example.test"
PASSWORD="hunter22-$(openssl rand -hex 8)"

log "registering throwaway user $EMAIL"
register_resp="$(curl_auth -sf -X POST "$AUTH_URL/api/v1/auth/register" \
	-H 'Content-Type: application/json' \
	-d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"full_name\":\"Privacy Export Drill\"}")"
TOKEN="$(echo "$register_resp" | json_field data.tokens.access_token)"
[ -n "$TOKEN" ] || fail "register did not return an access token: $register_resp"
ok "registered and received a real JWT"

log "requesting export (password re-verified)"
create_resp="$(curl_auth -sf -X POST "$AUTH_URL/api/v1/users/me/privacy/exports" \
	-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
	-d "{\"password\":\"$PASSWORD\"}")"
REQUEST_ID="$(echo "$create_resp" | json_field data.id)"
[ -n "$REQUEST_ID" ] || fail "create export did not return a request id: $create_resp"
ok "export request created: $REQUEST_ID"

log "confirming a duplicate request returns the SAME id (K9 idempotency)"
dup_resp="$(curl_auth -sf -X POST "$AUTH_URL/api/v1/users/me/privacy/exports" \
	-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
	-d "{\"password\":\"$PASSWORD\"}")"
DUP_ID="$(echo "$dup_resp" | json_field data.id)"
[ "$DUP_ID" = "$REQUEST_ID" ] || fail "duplicate export request returned a DIFFERENT id ($DUP_ID != $REQUEST_ID)"
ok "duplicate request correctly returned the same active export"

log "polling export status until ready (worker poll interval ~15s)"
STATUS=""
for _ in $(seq 1 40); do
	status_resp="$(curl_auth -sf "$AUTH_URL/api/v1/users/me/privacy/requests/$REQUEST_ID" -H "Authorization: Bearer $TOKEN")"
	STATUS="$(echo "$status_resp" | json_field data.status)"
	[ "$STATUS" = "ready" ] && break
	[ "$STATUS" = "failed" ] && fail "export assembly failed: $status_resp"
	sleep 2
done
[ "$STATUS" = "ready" ] || fail "export never reached 'ready' within the poll budget (last status: $STATUS)"
ok "export is ready"

log "downloading (password re-verified again)"
ARCHIVE="$WORKDIR/export.zip"
curl_auth -sf "$AUTH_URL/api/v1/users/me/privacy/exports/$REQUEST_ID/download" \
	-H "Authorization: Bearer $TOKEN" -H "X-Export-Password: $PASSWORD" \
	-o "$ARCHIVE"
[ -s "$ARCHIVE" ] || fail "downloaded archive is empty"
ok "downloaded $(du -h "$ARCHIVE" | cut -f1) archive"

log "confirming a second download attempt is refused (one-time download)"
second_status="$(curl_auth -s -o /dev/null -w '%{http_code}' "$AUTH_URL/api/v1/users/me/privacy/exports/$REQUEST_ID/download" \
	-H "Authorization: Bearer $TOKEN" -H "X-Export-Password: $PASSWORD")"
[ "$second_status" = "409" ] || fail "second download attempt returned $second_status, expected 409"
ok "second download attempt correctly refused (409)"

log "inspecting archive METADATA only (never printing file contents)"
unzip -l "$ARCHIVE" | tee "$WORKDIR/listing.txt" >/dev/null
grep -q "manifest.json" "$WORKDIR/listing.txt" || fail "archive is missing manifest.json"
grep -q "auth.ndjson" "$WORKDIR/listing.txt" || fail "archive is missing auth.ndjson"
ok "archive contains manifest.json + auth.ndjson"

unzip -p "$ARCHIVE" manifest.json > "$WORKDIR/manifest.json"
SCHEMA_VERSION="$(json_field schema_version < "$WORKDIR/manifest.json")"
ROW_COUNT="$(json_field owners < "$WORKDIR/manifest.json" | head -c 200)"
log "manifest schema_version=$SCHEMA_VERSION owners=$ROW_COUNT"
ok "privacy-export.sh completed successfully for request $REQUEST_ID"
