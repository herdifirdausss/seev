#!/usr/bin/env bash
# Full-container smoke test — automates the doc 34-T3 manual round-trip:
# `docker compose --profile app up` (real Docker images, not host binaries)
# → register + login a real user via auth → topup intent via gateway →
# signed mockvendor webhook → poll until settled → assert balance via
# `docker exec psql`. This is the CONTAINER counterpart to
# scripts/smoke-test.sh (which builds and runs six binaries on the HOST
# against Compose Postgres/Redis/RabbitMQ only) — docs/plan/44 Task T1/K4.
#
# Deliberately standalone: does NOT source scripts/lib.sh and does not
# start/stop host binaries. lib.sh's lifecycle (build_server, start_services,
# gen_token, psql_exec against $DB_USER) is for the HOST-BINARY gate; mixing
# the two process models in one script has bitten this repo before (see
# PROJECT_GUIDE.md's "one debug script, not repeated source scripts/lib.sh"
# gotcha) — this script's container lifecycle is self-contained instead.
#
# Usage:
#   ./scripts/smoke-container.sh                  # build images locally, run smoke
#   SEEV_SMOKE_NO_BUILD=1 ./scripts/smoke-container.sh   # images already loaded (CI after Bake)
#   SEEV_SMOKE_ARTIFACT_DIR=/path ./scripts/smoke-container.sh  # where failure diagnostics land
#
# Requires: docker, docker compose v2, curl, openssl. Fails fast if missing.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# ─── Dependency check (fail-fast, K4 step 1) ────────────────────────────────

for bin in docker curl openssl; do
	command -v "$bin" >/dev/null 2>&1 || {
		echo "smoke-container: required dependency '$bin' not found in PATH" >&2
		exit 2
	}
done
docker compose version >/dev/null 2>&1 || {
	echo "smoke-container: 'docker compose' (v2 plugin) not available" >&2
	exit 2
}

# ─── Config ──────────────────────────────────────────────────────────────────

ARTIFACT_DIR="${SEEV_SMOKE_ARTIFACT_DIR:-$ROOT_DIR/.smoke-container-artifacts}"
HEALTH_DEADLINE_SECS="${SEEV_SMOKE_HEALTH_DEADLINE:-180}"
SETTLE_DEADLINE_SECS="${SEEV_SMOKE_SETTLE_DEADLINE:-30}"

# Expected app-profile services (3 infra + 6 app = 9) — K4 step 4 requires
# asserting this EXACT set, not just "however many containers the project
# happens to report" (a stale container from an unrelated profile must not
# silently count as one of the nine).
EXPECTED_SERVICES=(postgres redis rabbitmq ledger-service auth-service payin-service payout-service fraud-service gateway-service)

# Per-run credentials (K6) — generated fresh, never committed, never logged
# raw. docker compose picks these up as env overrides for the `app` profile
# services (matching their `${VAR:-dummy-default}` fallback pattern in
# docker-compose.yml).
export JWT_SECRET="${JWT_SECRET:-$(openssl rand -hex 32)}"
export INTERNAL_GRPC_TOKEN="${INTERNAL_GRPC_TOKEN:-$(openssl rand -hex 32)}"
export VENDOR_MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-$(openssl rand -hex 32)}"
export MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-$(openssl rand -hex 32)}"
export AUTH_BOOTSTRAP_ADMIN_PASSWORD="${AUTH_BOOTSTRAP_ADMIN_PASSWORD:-$(openssl rand -hex 16)}"

FAILED=0
log() { printf '\033[1;34m[smoke-container]\033[0m %s\n' "$*"; }
ok() { printf '\033[1;32m[ pass]\033[0m %s\n' "$*"; }
fail() {
	printf '\033[1;31m[ FAIL]\033[0m %s\n' "$*" >&2
	FAILED=1
}

json_field() {
	sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"
}

# ─── Cleanup (K4 step 8: trap always tears down, preserves exit code) ───────

cleanup() {
	local exit_code=$?
	if [ "$exit_code" -ne 0 ] || [ "$FAILED" -ne 0 ]; then
		save_diagnostics
	fi
	log "tearing down: docker compose --profile app down -v --remove-orphans"
	docker compose --profile app down -v --remove-orphans >/dev/null 2>&1 || true
	exit "$exit_code"
}
trap cleanup EXIT

# save_diagnostics (K4 step 7) — allowlisted only: compose ps, health
# inspect, and container logs. Never binaries, .env, git metadata, or a
# full environment dump (K6's artifact allowlist).
save_diagnostics() {
	log "saving failure diagnostics to $ARTIFACT_DIR"
	mkdir -p "$ARTIFACT_DIR"
	docker compose --profile app ps >"$ARTIFACT_DIR/compose-ps.txt" 2>&1 || true
	for svc in "${EXPECTED_SERVICES[@]}"; do
		local cid
		cid="$(docker compose --profile app ps -q "$svc" 2>/dev/null || true)"
		if [ -n "$cid" ]; then
			docker inspect "$cid" --format '{{json .State.Health}}' >"$ARTIFACT_DIR/health-$svc.json" 2>&1 || true
			docker logs "$cid" >"$ARTIFACT_DIR/logs-$svc.txt" 2>&1 || true
		fi
	done
}

# ─── Lifecycle (K4 steps 2-3) ────────────────────────────────────────────────

log "resetting: docker compose --profile app down -v --remove-orphans"
docker compose --profile app down -v --remove-orphans >/dev/null 2>&1 || true

if [ "${SEEV_SMOKE_NO_BUILD:-0}" = "1" ]; then
	log "starting profile 'app' with pre-loaded images (--no-build)..."
	SEEV_IMAGE_TAG="${SEEV_IMAGE_TAG:-ci}" docker compose --profile app up --no-build -d
else
	log "building and starting profile 'app'..."
	SEEV_IMAGE_TAG="${SEEV_IMAGE_TAG:-dev}" docker compose --profile app up --build -d
fi

# ─── Poll health (K4 step 4) ─────────────────────────────────────────────────

log "waiting for ${#EXPECTED_SERVICES[@]} services to report healthy (deadline ${HEALTH_DEADLINE_SECS}s)..."
deadline=$((SECONDS + HEALTH_DEADLINE_SECS))
healthy_count=0
while [ "$SECONDS" -lt "$deadline" ]; do
	healthy_count=0
	for svc in "${EXPECTED_SERVICES[@]}"; do
		cid="$(docker compose --profile app ps -q "$svc" 2>/dev/null || true)"
		[ -z "$cid" ] && continue
		status="$(docker inspect "$cid" --format '{{.State.Health.Status}}' 2>/dev/null || echo "")"
		[ "$status" = "healthy" ] && healthy_count=$((healthy_count + 1))
	done
	[ "$healthy_count" -eq "${#EXPECTED_SERVICES[@]}" ] && break
	sleep 2
done

if [ "$healthy_count" -eq "${#EXPECTED_SERVICES[@]}" ]; then
	ok "all ${#EXPECTED_SERVICES[@]} expected services are healthy"
else
	fail "only $healthy_count/${#EXPECTED_SERVICES[@]} expected services became healthy within ${HEALTH_DEADLINE_SECS}s"
	docker compose --profile app ps
	exit 1
fi

# Assert EXACTLY the expected set is what the 'app' profile DEFINES — not
# what `docker compose ps` currently shows running. `docker compose ps`
# (with or without --profile) lists every container in the whole PROJECT
# regardless of profile — confirmed empirically: on a machine that also has
# the `observability` profile containers up (docs/plan/43), `ps --services
# --status running` returned alloy/grafana/loki/prometheus/tempo alongside
# the 9 app-profile services, which would have made this assertion falsely
# fail. `config --services` is the STATIC definition instead — immune to
# whatever else happens to be running in the same Compose project — and is
# exactly what "the app profile defines precisely these nine services"
# means; each service's own runtime health was already verified above,
# individually, by name.
defined_services="$(docker compose --profile app config --services | sort)"
expected_sorted="$(printf '%s\n' "${EXPECTED_SERVICES[@]}" | sort)"
if [ "$defined_services" = "$expected_sorted" ]; then
	ok "'app' profile defines exactly the expected nine services"
else
	fail "'app' profile service set does not match expected: got [$defined_services] want [$expected_sorted]"
fi

# ─── Round-trip: register, login, topup, webhook, settle (K4 step 5) ───────

EMAIL="smoke-container-$(date +%s)@example.com"
PASSWORD="SmokeContainer!2026"

log "POST /api/v1/auth/register via auth-service (:8082)..."
reg="$(curl -s -X POST "http://127.0.0.1:8082/api/v1/auth/register" \
	-H "Content-Type: application/json" \
	-d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"full_name\":\"Smoke Container\"}")"
USER_ID="$(echo "$reg" | json_field id)"
if [[ "$USER_ID" =~ ^[0-9a-fA-F-]{36}$ ]]; then
	ok "user registered ($USER_ID)"
else
	fail "registration did not return a valid UUID: $reg"
	exit 1
fi

log "POST /api/v1/auth/login..."
login="$(curl -s -X POST "http://127.0.0.1:8082/api/v1/auth/login" \
	-H "Content-Type: application/json" \
	-d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")"
ACCESS_TOKEN="$(echo "$login" | json_field access_token)"
REFRESH_TOKEN="$(echo "$login" | json_field refresh_token)"
[ -n "$ACCESS_TOKEN" ] && ok "logged in with a real JWT" || { fail "login failed: $login"; exit 1; }

log "clearing the KYC gate (L1, mock provider auto-approves)..."
curl -s -o /dev/null -X POST "http://127.0.0.1:8082/api/v1/users/me/kyc" \
	-H "Authorization: Bearer $ACCESS_TOKEN" -H "Content-Type: application/json" \
	-d '{"level_requested":1}'
refresh="$(curl -s -X POST "http://127.0.0.1:8082/api/v1/auth/refresh" \
	-H "Content-Type: application/json" -d "{\"refresh_token\":\"$REFRESH_TOKEN\"}")"
ACCESS_TOKEN="$(echo "$refresh" | json_field access_token)"
[ -n "$ACCESS_TOKEN" ] && ok "KYC L1 approved, token refreshed" || { fail "KYC L1 dance failed: $refresh"; exit 1; }

log "POST /api/v1/topup via gateway (:8080)..."
create="$(curl -s -X POST "http://127.0.0.1:8080/api/v1/topup" \
	-H "Authorization: Bearer $ACCESS_TOKEN" -H "Content-Type: application/json" \
	-d '{"amount":"500000"}')"
INTENT_ID="$(echo "$create" | json_field id)"
REFERENCE="$(echo "$create" | json_field reference)"
if [[ "$INTENT_ID" =~ ^[0-9a-fA-F-]{36}$ ]] && [ -n "$REFERENCE" ]; then
	ok "topup intent created (id=$INTENT_ID reference=$REFERENCE)"
else
	fail "topup intent creation failed or returned an invalid id: $create"
	exit 1
fi

log "POST /webhooks/mockvendor with a valid HMAC signature..."
BODY="{\"event_id\":\"smoke-container-$(date +%s)\",\"external_ref\":\"$REFERENCE\",\"user_id\":\"$(uuidgen | tr '[:upper:]' '[:lower:]')\",\"amount\":\"500000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
SIG="$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$VENDOR_MOCKVENDOR_SECRET" -r | awk '{print $1}')"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:8080/webhooks/mockvendor" \
	-H "X-Mock-Signature: $SIG" -H "Content-Type: application/json" -d "$BODY")
[ "${code:0:1}" = "2" ] && ok "signed webhook accepted (code=$code)" || { fail "webhook got $code, expected 2xx"; exit 1; }

log "polling topup intent until settled (deadline ${SETTLE_DEADLINE_SECS}s)..."
deadline=$((SECONDS + SETTLE_DEADLINE_SECS))
status=""
while [ "$SECONDS" -lt "$deadline" ]; do
	status="$(curl -s "http://127.0.0.1:8080/api/v1/topup/$INTENT_ID" -H "Authorization: Bearer $ACCESS_TOKEN" | json_field status)"
	[ "$status" = "settled" ] && break
	sleep 1
done
[ "$status" = "settled" ] && ok "topup intent settled" || { fail "topup intent status was '$status' after ${SETTLE_DEADLINE_SECS}s, expected 'settled'"; exit 1; }

# ─── Assert via docker exec psql (K4 step 6) ─────────────────────────────────
# UUID/amount are validated by regex BEFORE reaching SQL, and passed as a
# bound psql variable (-v), never string-interpolated from the raw curl
# response — the same discipline scripts/lib.sh's psql_exec callers use.

if ! [[ "$USER_ID" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]]; then
	fail "USER_ID failed UUID validation before SQL: $USER_ID"
	exit 1
fi

# `:variable` substitution is a psql front-end (client-side) feature that
# only runs on SQL psql itself SCANS — confirmed empirically that `-c` does
# NOT go through that scanner (a `:user_id` in a `-c` string reaches the
# server literally and gets "syntax error at or near ':'"), while the exact
# same query piped over STDIN works correctly. So the query is piped, not
# passed via -c, with -v still providing the bound (regex-validated, single-
# quoted) value.
BALANCE="$(printf "SELECT ab.balance FROM account_balances ab JOIN accounts a ON a.id = ab.account_id WHERE a.owner_id = :user_id AND a.type = 'cash';\n" |
	docker exec -i seev-postgres-1 psql -U seev -d seev_ledger -v ON_ERROR_STOP=1 -t -A -v user_id="'$USER_ID'")"
BALANCE="$(echo "$BALANCE" | tr -d '[:space:]')"
if [[ "$BALANCE" =~ ^[0-9]+$ ]] && [ "$BALANCE" = "500000" ]; then
	ok "cash balance is exactly 500000 after settlement (verified via docker exec psql)"
else
	fail "cash balance was '$BALANCE', expected 500000"
fi

echo
if [ "$FAILED" = "0" ]; then
	printf '\033[1;32m=== SMOKE-CONTAINER: ALL ASSERTIONS PASSED ===\033[0m\n'
	exit 0
else
	printf '\033[1;31m=== SMOKE-CONTAINER: ONE OR MORE ASSERTIONS FAILED ===\033[0m\n'
	exit 1
fi
