#!/usr/bin/env bash
# docs/roadmap/active/51-a8-data-lifecycle-privacy.md T6 (work item 2): exercises the FULL
# privacy surface end to end against an ALREADY-RUNNING app-profile stack —
# export, retention (status/dry-run), hold create/block/release, and
# account closure. Same "operator/local convenience tool, not a CI
# acceptance gate" posture as scripts/business-e2e.sh's own siblings
# (privacy-export.sh, admin-e2e.sh) — this script doesn't manage the
# stack's lifecycle itself.
#
# Reuses scripts/privacy-export.sh unchanged for the export leg rather
# than duplicating it. Reaches Postgres DIRECTLY (via cmd/retentionctl,
# the same admin CLI docs/roadmap/active/51 T1.6 built) for the retention/hold legs — those
# have no HTTP surface by design (K5's own "generic across every owner
# service rather than one endpoint per service").
#
# Usage:
#   AUTH_URL=http://localhost:8082 ./scripts/privacy-e2e.sh
#
# Requires: curl, unzip, jq (or python3), openssl, go (for retentionctl),
# and Postgres reachable at POSTGRES_HOST:POSTGRES_PORT with the
# credentials below (matches .env.example's own defaults — the app_service
# role, same one every retention worker runs as in production).
set -euo pipefail

AUTH_URL="${AUTH_URL:-http://localhost:8082}"
POSTGRES_HOST="${POSTGRES_HOST:-localhost}"
POSTGRES_PORT="${POSTGRES_PORT:-5433}"
POSTGRES_USER="${POSTGRES_USER:-seev_app}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-seev_app}"
AUTH_DSN="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${POSTGRES_PORT}/seev_auth?sslmode=disable"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

log()  { printf '\033[1;34m[privacy-e2e]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[ pass]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[ FAIL]\033[0m %s\n' "$*"; exit 1; }

json_field() {
	if command -v jq >/dev/null 2>&1; then
		jq -r ".$1 // empty"
	else
		python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1',''))"
	fi
}

retentionctl() {
	(cd "$REPO_ROOT" && go run ./cmd/retentionctl "$@")
}

register_user() {
	local email="$1" password="$2"
	local resp
	resp="$(curl -sf -X POST "$AUTH_URL/api/v1/auth/register" \
		-H 'Content-Type: application/json' \
		-d "{\"email\":\"$email\",\"password\":\"$password\",\"full_name\":\"Privacy E2E\"}")"
	echo "$resp" | json_field data.tokens.access_token
}

# ─── Leg 1: export (delegates to the existing, already-verified script) ───

log "=== leg 1/4: export ==="
AUTH_URL="$AUTH_URL" "$SCRIPT_DIR/privacy-export.sh"
ok "export leg complete (see above)"

# ─── Leg 2: retention status/dry-run visibility ───

log "=== leg 2/4: retention ==="
STATUS_OUT="$(retentionctl status --owner auth --dsn "$AUTH_DSN" --limit 5)"
echo "$STATUS_OUT" | grep -q "^owner=auth active_holds=" || fail "retentionctl status did not report auth's active_holds line"
ok "retentionctl status reachable: $(echo "$STATUS_OUT" | head -1)"

DRYRUN_OUT="$(retentionctl dry-run --owner auth --dsn "$AUTH_DSN" --class auth.refresh_tokens)"
echo "$DRYRUN_OUT" | grep -q "affected" || fail "retentionctl dry-run did not report an affected count"
ok "retentionctl dry-run reachable for auth.refresh_tokens (dry_run never mutates)"

# ─── Leg 3: hold create -> blocks closure -> release ───

log "=== leg 3/4: hold blocks closure, then release ==="
HOLD_EMAIL="privacy-e2e-hold-$(date +%s)-$RANDOM@example.test"
HOLD_PASSWORD="hunter22-$(openssl rand -hex 8)"
HOLD_TOKEN="$(register_user "$HOLD_EMAIL" "$HOLD_PASSWORD")"
[ -n "$HOLD_TOKEN" ] || fail "register (hold leg) did not return an access token"
HOLD_USER_ID="$(curl -sf "$AUTH_URL/api/v1/users/me" -H "Authorization: Bearer $HOLD_TOKEN" | json_field data.id)"
[ -n "$HOLD_USER_ID" ] || fail "could not resolve the hold-leg user's own id"
ok "registered hold-leg user $HOLD_USER_ID"

CREATE_HOLD_OUT="$(retentionctl hold-create --owner auth --dsn "$AUTH_DSN" \
	--scope subject --value "$HOLD_USER_ID" --reason legal_hold \
	--note "privacy-e2e drill" --created-by "privacy-e2e-drill")"
HOLD_ID="$(echo "$CREATE_HOLD_OUT" | sed -n 's/.*id=\([a-f0-9-]*\).*/\1/p')"
[ -n "$HOLD_ID" ] || fail "could not parse hold id from: $CREATE_HOLD_OUT"
ok "hold created: $HOLD_ID"

CLOSURE_CREATE="$(curl -sf -X POST "$AUTH_URL/api/v1/users/me/privacy/closure" \
	-H "Authorization: Bearer $HOLD_TOKEN" -H 'Content-Type: application/json' \
	-d "{\"password\":\"$HOLD_PASSWORD\"}")"
HOLD_REQUEST_ID="$(echo "$CLOSURE_CREATE" | json_field data.id)"
[ -n "$HOLD_REQUEST_ID" ] || fail "closure request (hold leg) did not return a request id: $CLOSURE_CREATE"
ok "closure requested while a hold is active: $HOLD_REQUEST_ID"

HOLD_STATUS=""
for _ in $(seq 1 20); do
	HOLD_STATUS="$(curl -sf "$AUTH_URL/api/v1/users/me/privacy/closure/$HOLD_REQUEST_ID" -H "Authorization: Bearer $HOLD_TOKEN" | json_field data.status)"
	[ "$HOLD_STATUS" = "blocked" ] && break
	sleep 2
done
[ "$HOLD_STATUS" = "blocked" ] || fail "closure never reached 'blocked' with an active hold (last status: $HOLD_STATUS)"
ok "closure correctly blocked by the active retention hold"

retentionctl hold-release --owner auth --dsn "$AUTH_DSN" --id "$HOLD_ID" --released-by "privacy-e2e-drill-releaser" >/dev/null
ok "hold released (by a DIFFERENT operator identity than the creator — K5)"

# ─── Leg 4: full closure happy path on a separate, hold-free user ───

log "=== leg 4/4: closure happy path ==="
CLOSE_EMAIL="privacy-e2e-close-$(date +%s)-$RANDOM@example.test"
CLOSE_PASSWORD="hunter22-$(openssl rand -hex 8)"
CLOSE_TOKEN="$(register_user "$CLOSE_EMAIL" "$CLOSE_PASSWORD")"
[ -n "$CLOSE_TOKEN" ] || fail "register (closure leg) did not return an access token"
ok "registered closure-leg user"

CLOSE_CREATE="$(curl -sf -X POST "$AUTH_URL/api/v1/users/me/privacy/closure" \
	-H "Authorization: Bearer $CLOSE_TOKEN" -H 'Content-Type: application/json' \
	-d "{\"password\":\"$CLOSE_PASSWORD\"}")"
CLOSE_REQUEST_ID="$(echo "$CLOSE_CREATE" | json_field data.id)"
[ -n "$CLOSE_REQUEST_ID" ] || fail "closure request did not return a request id: $CLOSE_CREATE"
ok "closure requested: $CLOSE_REQUEST_ID"

log "confirming login is rejected IMMEDIATELY (before any saga step has run)"
IMMEDIATE_LOGIN_STATUS="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$AUTH_URL/api/v1/auth/login" \
	-H 'Content-Type: application/json' -d "{\"email\":\"$CLOSE_EMAIL\",\"password\":\"$CLOSE_PASSWORD\"}")"
[ "$IMMEDIATE_LOGIN_STATUS" = "403" ] || fail "login after closure REQUEST (before saga runs) returned $IMMEDIATE_LOGIN_STATUS, expected 403"
ok "login correctly rejected immediately after closure request (status flip, not worker-dependent)"

CLOSE_STATUS=""
for _ in $(seq 1 40); do
	CLOSE_STATUS="$(curl -sf "$AUTH_URL/api/v1/users/me/privacy/closure/$CLOSE_REQUEST_ID" -H "Authorization: Bearer $CLOSE_TOKEN" | json_field data.status)"
	[ "$CLOSE_STATUS" = "completed" ] && break
	{ [ "$CLOSE_STATUS" = "blocked" ] || [ "$CLOSE_STATUS" = "dead" ]; } && fail "closure reached terminal failure status '$CLOSE_STATUS' unexpectedly"
	sleep 2
done
[ "$CLOSE_STATUS" = "completed" ] || fail "closure never reached 'completed' within the poll budget (last status: $CLOSE_STATUS)"
ok "closure completed"

log "confirming login is rejected AFTER completion (tombstoned identity)"
FINAL_LOGIN_STATUS="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$AUTH_URL/api/v1/auth/login" \
	-H 'Content-Type: application/json' -d "{\"email\":\"$CLOSE_EMAIL\",\"password\":\"$CLOSE_PASSWORD\"}")"
[ "$FINAL_LOGIN_STATUS" = "401" ] || fail "login after closure completion returned $FINAL_LOGIN_STATUS, expected 401 (tombstoned email is no longer a valid identifier)"
ok "login correctly rejected after completion (401 — identity tombstoned, not merely disabled)"

log "privacy-e2e.sh completed successfully: export, retention, hold, closure all verified"
