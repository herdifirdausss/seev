#!/usr/bin/env bash
# Repeatable admin-console journey (docs/roadmap/archive/47 T6). This intentionally uses
# lib.sh once and starts the BFF separately from the user-money gateway path.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
LIB_LOG_TAG="admin-e2e"
LIB_WORK_DIR_PREFIX="admin-e2e"
source "$ROOT_DIR/scripts/lib.sh"
trap cleanup EXIT

ADMIN_EMAIL="${ADMIN_E2E_EMAIL:-admin-e2e@example.com}"
ADMIN_PASSWORD="${ADMIN_E2E_PASSWORD:-admin-e2e-password-change-me-32chars}"
COOKIE_JAR="$WORK_DIR/admin.cookies"

# Bootstrap is idempotent and runs in auth-service's own database. The BFF
# login URL is public auth (:8082); admin KYC proxy traffic uses :8083.
export AUTH_BOOTSTRAP_ADMIN_EMAIL="$ADMIN_EMAIL"
export AUTH_BOOTSTRAP_ADMIN_PASSWORD="$ADMIN_PASSWORD"

ensure_deps_up
build_server
start_services
start_adminbff_service

log "login through admin BFF"
# Keep the 303 boundary explicit.  Forcing POST while following the redirect
# makes curl replay POST against /api/v1/admin/, where CSRF is correctly
# required, unlike a browser's GET after a form redirect.
curl_internal -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" -X POST "http://localhost:$ADMINBFF_PORT/login" \
	-H 'Content-Type: application/x-www-form-urlencoded' \
	--data-urlencode "email=$ADMIN_EMAIL" --data-urlencode "password=$ADMIN_PASSWORD" \
	-o "$WORK_DIR/login.response" -D "$WORK_DIR/login.headers"
login_page="$(curl_internal -fsS -b "$COOKIE_JAR" "http://localhost:$ADMINBFF_PORT/api/v1/admin/")"
csrf="$(printf '%s' "$login_page" | sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' | head -1)"
[ -n "$csrf" ] && ok "admin session established and CSRF token rendered" || fail "admin login did not render a CSRF token"

dashboard="$(curl_internal -fsS -b "$COOKIE_JAR" "http://localhost:$ADMINBFF_PORT/api/v1/admin/")"
printf '%s' "$dashboard" | grep -q 'Seev Admin' && ok "protected dashboard is reachable" || fail "protected dashboard response missing console marker"

before="$(psql_exec "$ADMINBFF_DB_NAME" -c "SELECT count(*) FROM audit_log;" | tr -d '[:space:]')"
code="$(curl_internal -sS -o "$WORK_DIR/replay.json" -w '%{http_code}' -b "$COOKIE_JAR" -X POST \
	"http://localhost:$ADMINBFF_PORT/api/v1/admin/payout/vendor-commands/dead/replay-all" \
	-H 'Content-Type: application/x-www-form-urlencoded' --data-urlencode "csrf_token=$csrf")"
[ "$code" = "200" ] && ok "payout dead-command replay endpoint accepted a CSRF-protected mutation" || fail "replay-all returned HTTP $code"

after="$(psql_exec "$ADMINBFF_DB_NAME" -c "SELECT count(*) FROM audit_log;" | tr -d '[:space:]')"
[ "$after" -gt "$before" ] && ok "BFF audit row recorded for mutation" || fail "audit row count did not increase ($before -> $after)"

curl_internal -fsS -b "$COOKIE_JAR" "http://localhost:$ADMINBFF_PORT/api/v1/admin/catalog" | grep -q 'Audit log' \
	&& ok "batch-2 operations panels render" || fail "batch-2 operations panel missing"

if [ "${FAILED:-0}" -ne 0 ]; then
	exit 1
fi
log "admin-e2e completed"
