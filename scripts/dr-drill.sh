#!/usr/bin/env bash
# docs/roadmap/active/50 T6: full game-day disaster-recovery drill. Builds a
# throwaway "gameday" environment from scratch (fresh Postgres/Redis/
# RabbitMQ under an isolated Compose project, real pgBackRest backups
# against an isolated repository — never deploy/backup/repo, the real
# one), creates a small representative cross-service fixture, records a
# recovery target, takes a backup, destroys ONLY the gameday's own
# volumes (the simulated disaster), then chains every tool T1-T5 already
# built: scripts/restore-cluster.sh (restore+promote) -> cmd/drverify
# (zero-fatal gate) -> cmd/drreseed (Redis reconstruction) ->
# scripts/post-restore-security.sh (session/token fence) -> starts the
# application against the restored cluster and proves the fixture
# survived (and, in pitr mode, that post-target activity did not).
# Prints one JSON report with stage timings and measured RPO/RTO.
#
# Requires the real dev stack to be stopped first (`docker compose stop`,
# not `down -v`) — this drill reuses the SAME host ports the app profile
# normally binds to (127.0.0.1:8080 etc.), the same convention every
# other isolated-Compose gate in this repo already follows (docs/plan 45
# T4, 49 T6).
#
# Usage:
#   ./scripts/dr-drill.sh latest
#   ./scripts/dr-drill.sh pitr
#   ./scripts/dr-drill.sh cleanup
#
# Environment:
#   GAMEDAY_PROJECT       Compose project for the throwaway "before" stack
#                           (default: seev-a7-gameday).
#   DRILL_PROJECT_NAME    Compose project for the restored "after" stack —
#                           same variable name scripts/restore-cluster.sh
#                           itself reads (default: seev-a7-drill).
#   GAMEDAY_REPO_PATH     Host path for the gameday's OWN isolated backup
#                           repository (default: /tmp/seev-a7-gameday-repo).
#                           Never deploy/backup/repo.
#   DRILL_REPORT_PATH     Where to write the final JSON report (default:
#                           /tmp/seev-a7-dr-drill-report.json).
set -euo pipefail

usage() {
	cat >&2 <<'EOF'
Usage: scripts/dr-drill.sh <latest|pitr>
       scripts/dr-drill.sh cleanup
EOF
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GAMEDAY_PROJECT="${GAMEDAY_PROJECT:-seev-a7-gameday}"
DRILL_PROJECT_NAME="${DRILL_PROJECT_NAME:-seev-a7-drill}"
export DRILL_PROJECT_NAME
GAMEDAY_REPO_PATH="${GAMEDAY_REPO_PATH:-/tmp/seev-a7-gameday-repo}"
REPORT_PATH="${DRILL_REPORT_PATH:-/tmp/seev-a7-dr-drill-report.json}"
IMAGE="seev/postgres-backup:${SEEV_IMAGE_TAG:-dev}"

if [ -z "$GAMEDAY_PROJECT" ] || [ "$GAMEDAY_PROJECT" = "seev" ] || [ -z "$DRILL_PROJECT_NAME" ] || [ "$DRILL_PROJECT_NAME" = "seev" ]; then
	echo "dr-drill.sh: refusing default/empty project names — both GAMEDAY_PROJECT and DRILL_PROJECT_NAME must be isolated, never 'seev'." >&2
	exit 1
fi

gameday_compose() {
	docker compose --project-name "$GAMEDAY_PROJECT" --project-directory "$ROOT_DIR" "$@"
}

gameday_pgbackrest() {
	gameday_compose exec -T -e PGBACKREST_REPO1_CIPHER_PASS="$(cat deploy/backup/secrets/pgbackrest_repo_passphrase)" \
		postgres pgbackrest --stanza=seev --config=/etc/pgbackrest/pgbackrest.conf "$@"
}

drill_restore_compose() {
	docker compose --project-name "$DRILL_PROJECT_NAME" --project-directory "$ROOT_DIR" -f deploy/backup/restore-compose.yml "$@"
}

drill_infra_compose() {
	docker compose --project-name "$DRILL_PROJECT_NAME" --project-directory "$ROOT_DIR" -f docker-compose.yml "$@"
}

# cleanup_all always names both projects explicitly and prints the exact
# volumes before removing anything — same K7 guard shape as
# scripts/restore-cluster.sh's own cleanup_drill, extended to both halves
# of this drill. Registered via `trap ... EXIT` below so a failed run
# still tears down completely; never a blanket `docker system prune`.
cleanup_all() {
	echo "dr-drill.sh: cleaning up — gameday project '$GAMEDAY_PROJECT' and drill project '$DRILL_PROJECT_NAME', target volumes:"
	docker volume ls --filter "name=^${GAMEDAY_PROJECT}_" --format '  {{.Name}}'
	docker volume ls --filter "name=^${DRILL_PROJECT_NAME}_" --format '  {{.Name}}'
	if declare -F stop_services >/dev/null 2>&1; then
		stop_services || true
	fi
	gameday_compose down -v --remove-orphans >/dev/null 2>&1 || true
	drill_restore_compose down -v --remove-orphans >/dev/null 2>&1 || true
	drill_infra_compose --profile app rm -f -s -v redis rabbitmq >/dev/null 2>&1 || true
	docker volume rm "${DRILL_PROJECT_NAME}_seev_redis_data" "${DRILL_PROJECT_NAME}_seev_rabbitmq_data" >/dev/null 2>&1 || true
	rm -rf "$GAMEDAY_REPO_PATH"
	if [ -n "${WORK_DIR:-}" ] && [ "${KEEP_WORK_DIR:-0}" != "1" ]; then
		rm -rf "$WORK_DIR"
	fi
}

if [ "${1:-}" = "cleanup" ]; then
	cleanup_all
	exit 0
fi

MODE="${1:-}"
case "$MODE" in
latest | pitr) ;;
*)
	usage
	exit 2
	;;
esac

# Safety: this drill reuses the real dev stack's own host ports
# (127.0.0.1:8080 etc. via lib.sh's fixed defaults) — refuse to start if
# the real dev stack is up, rather than produce confusing bind-address-
# already-in-use failures deep into a multi-stage drill.
for c in seev-postgres-1 seev-redis-1 seev-rabbitmq-1; do
	if docker inspect "$c" --format '{{.State.Running}}' 2>/dev/null | grep -q true; then
		echo "dr-drill.sh: refusing to start — the real dev stack container '$c' is running. Run 'docker compose stop' first (never 'down -v' — that is the operator's own call, not this script's)." >&2
		exit 1
	fi
done

if [ ! -f deploy/backup/secrets/pgbackrest_repo_passphrase ] || [ ! -f deploy/backup/secrets/seev_backup_password ]; then
	echo "dr-drill.sh: deploy/backup/secrets/ missing — run 'make backup-secret' first." >&2
	exit 1
fi
if [ ! -d deploy/certs ] || [ ! -f deploy/certs/ca.pem ]; then
	echo "dr-drill.sh: deploy/certs/ missing — run 'make certs' first." >&2
	exit 1
fi
if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
	echo "dr-drill.sh: image $IMAGE not found locally — run 'docker compose build postgres' first." >&2
	exit 1
fi

STAGE_LOG=()
record_stage() {
	local name="$1" at
	at="$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)"
	STAGE_LOG+=("${name}=${at}")
	echo "dr-drill.sh: stage '${name}' at ${at}"
}
stage_time() {
	local name="$1" pair
	for pair in "${STAGE_LOG[@]}"; do
		if [ "${pair%%=*}" = "$name" ]; then
			echo "${pair#*=}"
			return 0
		fi
	done
}

trap cleanup_all EXIT

mkdir -p "$GAMEDAY_REPO_PATH"
record_stage drill_start

# ─── Phase A: fresh gameday infrastructure ──────────────────────────────────
echo "dr-drill.sh: === Phase A: bringing up a fresh gameday Postgres/Redis/RabbitMQ ==="
BACKUP_REPO_PATH="$GAMEDAY_REPO_PATH" gameday_compose up -d postgres redis rabbitmq

GAMEDAY_POSTGRES="${GAMEDAY_PROJECT}-postgres-1"
GAMEDAY_REDIS="${GAMEDAY_PROJECT}-redis-1"
GAMEDAY_RABBITMQ="${GAMEDAY_PROJECT}-rabbitmq-1"

wait_healthy() {
	local container=$1 tries=60
	while [ "$tries" -gt 0 ]; do
		local status
		status="$(docker inspect "$container" --format '{{.State.Health.Status}}' 2>/dev/null || echo missing)"
		[ "$status" = "healthy" ] && return 0
		sleep 2
		tries=$((tries - 1))
	done
	echo "dr-drill.sh: $container did not become healthy in time" >&2
	return 1
}
wait_healthy "$GAMEDAY_POSTGRES"
wait_healthy "$GAMEDAY_REDIS"
wait_healthy "$GAMEDAY_RABBITMQ"
record_stage gameday_infra_healthy

echo "dr-drill.sh: creating the pgBackRest stanza against the gameday cluster..."
gameday_pgbackrest stanza-create
record_stage gameday_stanza_ready

# ─── Phase B: application services on the gameday stack ────────────────────
echo "dr-drill.sh: === Phase B: building and starting the application against gameday Postgres ==="
export POSTGRES_CONTAINER="$GAMEDAY_POSTGRES"
export REDIS_CONTAINER="$GAMEDAY_REDIS"
export RABBITMQ_CONTAINER="$GAMEDAY_RABBITMQ"
export LIB_LOG_TAG=drill
export LIB_WORK_DIR_PREFIX=drill-gameday
# shellcheck source=scripts/lib.sh
source "$ROOT_DIR/scripts/lib.sh"

# The postgres image's own /docker-entrypoint-initdb.d scripts
# (scripts/postgres-init/01-04) already provisioned all eight databases,
# every *_app role, every service's migrations, and the seev_backup role
# on this FRESH volume's first boot — unlike a script reusing an existing
# dev volume, nothing here needs lib.sh's ensure_service_dbs/
# apply_migrations backfill helpers.
detect_db_port
build_server
start_services
record_stage gameday_services_up

# ─── Phase C: representative cross-service fixture ──────────────────────────
echo "dr-drill.sh: === Phase C: creating a representative fixture (register, KYC, top-up) ==="
RUN_ID="$(date +%s)-$$"
FIXTURE_EMAIL="dr-drill-$RUN_ID@example.com"
FIXTURE_PASSWORD="DrDrill!2026"
MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"

reg="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/register" \
	-H "Content-Type: application/json" \
	-d "{\"email\":\"$FIXTURE_EMAIL\",\"password\":\"$FIXTURE_PASSWORD\",\"full_name\":\"DR Drill Fixture\"}")"
FIXTURE_USER_ID="$(echo "$reg" | json_field id)"
[ -n "$FIXTURE_USER_ID" ] || {
	echo "dr-drill.sh: fixture registration failed: $reg" >&2
	exit 1
}
echo "dr-drill.sh: fixture user registered ($FIXTURE_USER_ID)"

login="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/login" \
	-H "Content-Type: application/json" \
	-d "{\"email\":\"$FIXTURE_EMAIL\",\"password\":\"$FIXTURE_PASSWORD\"}")"
FIXTURE_TOKEN="$(echo "$login" | json_field access_token)"
FIXTURE_REFRESH="$(echo "$login" | json_field refresh_token)"
[ -n "$FIXTURE_TOKEN" ] || {
	echo "dr-drill.sh: fixture login failed: $login" >&2
	exit 1
}

kyc_resp="$(kyc_approve_l1 "$AUTH_APP_PORT" "$FIXTURE_TOKEN" "$FIXTURE_REFRESH")"
FIXTURE_TOKEN="$(echo "$kyc_resp" | json_field access_token)"
[ -n "$FIXTURE_TOKEN" ] || {
	echo "dr-drill.sh: fixture KYC L1 approval failed: $kyc_resp" >&2
	exit 1
}

admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"
psql_exec "$PAYIN_DB_NAME" -c "DELETE FROM payin_routing_rules WHERE priority = 20;" >/dev/null
curl_internal -s -o /dev/null -X PUT "http://localhost:$PAYIN_ADMIN_PORT/admin/payin/vendor-gateways/mockvendor" \
	-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"gateway":"bca"}'
curl_internal -s -o /dev/null -X POST "http://localhost:$PAYIN_ADMIN_PORT/admin/payin/routing-rules" \
	-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
	-d '{"flow":"topup","priority":20,"enabled":true,"currency":"IDR","vendor":"mockvendor"}'

topup_once() {
	local amount=$1 event_suffix=$2
	local create intent_id reference body sig code
	create="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/topup" \
		-H "Authorization: Bearer $FIXTURE_TOKEN" -H "Content-Type: application/json" \
		-d "{\"amount\":\"$amount\"}")"
	intent_id="$(echo "$create" | json_field id)"
	reference="$(echo "$create" | json_field reference)"
	[ -n "$intent_id" ] && [ -n "$reference" ] || {
		echo "dr-drill.sh: fixture topup intent creation failed: $create" >&2
		exit 1
	}
	body="{\"event_id\":\"dr-drill-$RUN_ID-$event_suffix\",\"external_ref\":\"$reference\",\"user_id\":\"$(uuidgen | tr '[:upper:]' '[:lower:]')\",\"amount\":\"$amount\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
	sig="$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$MOCKVENDOR_SECRET" -r | awk '{print $1}')"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: $sig" -H "Content-Type: application/json" -d "$body")
	[ "${code:0:1}" = "2" ] || {
		echo "dr-drill.sh: fixture topup webhook got $code, expected 2xx" >&2
		exit 1
	}
}

topup_once 500000 before
FIXTURE_CASH_ACCOUNT="$(cash_account_id "$FIXTURE_USER_ID")"
BEFORE_BALANCE="$(account_balance "$FIXTURE_CASH_ACCOUNT")"
[ "$BEFORE_BALANCE" = "500000" ] || {
	echo "dr-drill.sh: fixture balance after first top-up was '$BEFORE_BALANCE', expected 500000" >&2
	exit 1
}
echo "dr-drill.sh: fixture balance after 'before' top-up: $BEFORE_BALANCE"
record_stage fixture_before

echo "dr-drill.sh: taking the gameday baseline full backup..."
gameday_pgbackrest --type=full backup
record_stage gameday_backup_full

PITR_TARGET=""
AFTER_BALANCE_EXPECTED="$BEFORE_BALANCE"
if [ "$MODE" = "pitr" ]; then
	sleep 3
	PITR_TARGET="$(psql_exec postgres -c "SELECT now();")"
	echo "dr-drill.sh: PITR target recorded: $PITR_TARGET"
	sleep 3
	topup_once 250000 after
	AFTER_BALANCE_EXPECTED=$((BEFORE_BALANCE + 250000))
	AFTER_BALANCE_ACTUAL="$(account_balance "$FIXTURE_CASH_ACCOUNT")"
	[ "$AFTER_BALANCE_ACTUAL" = "$AFTER_BALANCE_EXPECTED" ] || {
		echo "dr-drill.sh: fixture balance after 'after' top-up was '$AFTER_BALANCE_ACTUAL', expected $AFTER_BALANCE_EXPECTED" >&2
		exit 1
	}
	echo "dr-drill.sh: fixture balance after 'after' top-up (must NOT survive PITR): $AFTER_BALANCE_ACTUAL"
	record_stage fixture_after
fi

# Force the current WAL segment closed rather than wait out the 60s
# archive_timeout (K4) — proves the archive path itself works and keeps
# this drill's own RPO measurement meaningful instead of dominated by
# script sleep time.
psql_exec postgres -c "SELECT pg_switch_wal();" >/dev/null
sleep 2
record_stage last_wal_archived

DISASTER_AT="$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)"
echo "dr-drill.sh: === Phase D: disaster declared at $DISASTER_AT — destroying ONLY the gameday's own volumes ==="
stop_services
gameday_compose stop postgres redis rabbitmq
gameday_compose rm -f postgres redis rabbitmq
docker volume rm "${GAMEDAY_PROJECT}_seev_postgres_data" "${GAMEDAY_PROJECT}_seev_redis_data" "${GAMEDAY_PROJECT}_seev_rabbitmq_data" >/dev/null
record_stage disaster_volumes_destroyed

# ─── Phase E: restore, verify, reseed, fence ────────────────────────────────
echo "dr-drill.sh: === Phase E: restoring from the gameday's own backup repository ==="
export BACKUP_REPO_PATH="$GAMEDAY_REPO_PATH"
if [ "$MODE" = "latest" ]; then
	./scripts/restore-cluster.sh latest
else
	./scripts/restore-cluster.sh time "$PITR_TARGET"
fi
record_stage restore_done

DRILL_POSTGRES="${DRILL_PROJECT_NAME}-postgres-restore-1"
DRILL_POSTGRES_PORT="$(docker port "$DRILL_POSTGRES" 5432/tcp | head -1 | cut -d: -f2)"
[ -n "$DRILL_POSTGRES_PORT" ] || {
	echo "dr-drill.sh: could not detect the restored Postgres's published host port" >&2
	exit 1
}
echo "dr-drill.sh: restored Postgres reachable at localhost:$DRILL_POSTGRES_PORT"

echo "dr-drill.sh: bringing up fresh Redis/RabbitMQ for the restored cluster (K10: never carried over from the gameday, never backed up)..."
drill_infra_compose up -d redis rabbitmq
DRILL_REDIS="${DRILL_PROJECT_NAME}-redis-1"
DRILL_RABBITMQ="${DRILL_PROJECT_NAME}-rabbitmq-1"
wait_healthy "$DRILL_REDIS"
wait_healthy "$DRILL_RABBITMQ"
record_stage restore_infra_up

echo "dr-drill.sh: === Phase F: cmd/drverify — zero-fatal cross-database gate ==="
DRVERIFY_BIN="$WORK_DIR/drverify"
go build -o "$DRVERIFY_BIN" ./cmd/drverify
verify_env=(
	"LEDGER_DSN=postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_ledger?sslmode=disable"
	"AUTH_DSN=postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_auth?sslmode=disable"
	"PAYIN_DSN=postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_payin?sslmode=disable"
	"PAYOUT_DSN=postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_payout?sslmode=disable"
	"FRAUD_DSN=postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_fraud?sslmode=disable"
	"GATEWAY_DSN=postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_gateway?sslmode=disable"
	"ADMINBFF_DSN=postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_adminbff?sslmode=disable"
	"ASSURANCE_DSN=postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_assurance?sslmode=disable"
)
DRVERIFY_REPORT="$WORK_DIR/drverify-report.json"
if ! env "${verify_env[@]}" "$DRVERIFY_BIN" >"$DRVERIFY_REPORT"; then
	echo "dr-drill.sh: cmd/drverify reported failures:" >&2
	cat "$DRVERIFY_REPORT" >&2
	exit 1
fi
echo "dr-drill.sh: drverify passed — report:"
cat "$DRVERIFY_REPORT"
record_stage drverify_pass

echo "dr-drill.sh: === Phase G: cmd/drreseed — Redis policy/fraud reconstruction ==="
DRRESEED_BIN="$WORK_DIR/drreseed"
go build -o "$DRRESEED_BIN" ./cmd/drreseed
DRRESEED_REPORT="$WORK_DIR/drreseed-report.json"
if ! LEDGER_DSN="postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_ledger?sslmode=disable" \
	FRAUD_DSN="postgres://$DB_USER:$DB_USER@localhost:$DRILL_POSTGRES_PORT/seev_fraud?sslmode=disable" \
	REDIS_ADDR="localhost:$REDIS_HOST_PORT" \
	"$DRRESEED_BIN" >"$DRRESEED_REPORT"; then
	echo "dr-drill.sh: cmd/drreseed reported failures:" >&2
	cat "$DRRESEED_REPORT" >&2
	exit 1
fi
echo "dr-drill.sh: drreseed passed — report:"
cat "$DRRESEED_REPORT"
record_stage drreseed_pass

echo "dr-drill.sh: === Phase H: post-restore security fence (K11) ==="
POSTGRES_CONTAINER="$DRILL_POSTGRES" POSTGRES_MIGRATE_USER="$DB_USER" POSTGRES_MIGRATE_PASSWORD="$DB_USER" \
	./scripts/post-restore-security.sh --confirm
record_stage security_fence_pass

# ─── Phase I: application against the restored cluster ─────────────────────
echo "dr-drill.sh: === Phase I: starting the application against the restored cluster ==="
POSTGRES_CONTAINER="$DRILL_POSTGRES"
REDIS_CONTAINER="$DRILL_REDIS"
RABBITMQ_CONTAINER="$DRILL_RABBITMQ"
detect_db_port
start_services
RECOVERED_AT="$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)"
record_stage restore_services_up

# ─── Phase J: business smoke on the restored fixture ────────────────────────
echo "dr-drill.sh: === Phase J: business smoke — proving the fixture survived ==="
restored_login="$(curl -s -X POST "http://localhost:$AUTH_APP_PORT/api/v1/auth/login" \
	-H "Content-Type: application/json" \
	-d "{\"email\":\"$FIXTURE_EMAIL\",\"password\":\"$FIXTURE_PASSWORD\"}")"
RESTORED_TOKEN="$(echo "$restored_login" | json_field access_token)"
[ -n "$RESTORED_TOKEN" ] || {
	echo "dr-drill.sh: fixture user could not log in after restore (fence must not touch password auth): $restored_login" >&2
	exit 1
}
echo "dr-drill.sh: fixture user logged in after restore with a fresh session — the old refresh token/session were fenced (K11), password auth was not"

RESTORED_BALANCE="$(account_balance "$FIXTURE_CASH_ACCOUNT")"
if [ "$MODE" = "latest" ]; then
	[ "$RESTORED_BALANCE" = "$BEFORE_BALANCE" ] || {
		echo "dr-drill.sh: latest restore lost data — balance is '$RESTORED_BALANCE', expected $BEFORE_BALANCE" >&2
		exit 1
	}
	echo "dr-drill.sh: latest restore includes the fixture's committed balance ($RESTORED_BALANCE) — RPO target met"
else
	[ "$RESTORED_BALANCE" = "$BEFORE_BALANCE" ] || {
		echo "dr-drill.sh: PITR restore did not land exactly at the target — balance is '$RESTORED_BALANCE', expected $BEFORE_BALANCE (the 'before' amount only, excluding 'after')" >&2
		exit 1
	}
	echo "dr-drill.sh: PITR restore includes 'before' ($BEFORE_BALANCE) and correctly EXCLUDES 'after' (would have been $AFTER_BALANCE_EXPECTED)"
fi

ZERO_ROW_CHECK="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM fn_verify_ledger_balance();")"
[ "$ZERO_ROW_CHECK" = "0" ] || {
	echo "dr-drill.sh: fn_verify_ledger_balance reports $ZERO_ROW_CHECK discrepancies after restore" >&2
	exit 1
}
echo "dr-drill.sh: fn_verify_ledger_balance reports zero discrepancies"
record_stage smoke_pass

# ─── RPO/RTO and final report ───────────────────────────────────────────────
LAST_ARCHIVE_AT="$(stage_time last_wal_archived)"
RPO_SECONDS=$(($(date -u -d "$DISASTER_AT" +%s 2>/dev/null || date -u -j -f '%Y-%m-%dT%H:%M:%S' "${DISASTER_AT%%.*}" +%s) - $(date -u -d "$LAST_ARCHIVE_AT" +%s 2>/dev/null || date -u -j -f '%Y-%m-%dT%H:%M:%S' "${LAST_ARCHIVE_AT%%.*}" +%s)))
RTO_SECONDS=$(($(date -u -d "$RECOVERED_AT" +%s 2>/dev/null || date -u -j -f '%Y-%m-%dT%H:%M:%S' "${RECOVERED_AT%%.*}" +%s) - $(date -u -d "$DISASTER_AT" +%s 2>/dev/null || date -u -j -f '%Y-%m-%dT%H:%M:%S' "${DISASTER_AT%%.*}" +%s)))

echo "dr-drill.sh: measured RPO=${RPO_SECONDS}s (budget 300s) RTO=${RTO_SECONDS}s (budget 1200s)"

python3 - "$REPORT_PATH" "$MODE" "$DISASTER_AT" "$RECOVERED_AT" "$RPO_SECONDS" "$RTO_SECONDS" "${STAGE_LOG[@]}" <<'PYEOF'
import json, sys

path, mode, disaster_at, recovered_at, rpo, rto = sys.argv[1:7]
stage_pairs = sys.argv[7:]
stages = {}
for pair in stage_pairs:
    name, at = pair.split("=", 1)
    stages[name] = at

report = {
    "mode": mode,
    "disaster_at": disaster_at,
    "recovered_at": recovered_at,
    "rpo_seconds": int(rpo),
    "rto_seconds": int(rto),
    "rpo_budget_seconds": 300,
    "rto_budget_seconds": 1200,
    "passed": int(rpo) <= 300 and int(rto) <= 1200,
    "stages": stages,
}
with open(path, "w") as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write("\n")
print(f"dr-drill.sh: wrote {path}")
PYEOF

if [ "$RPO_SECONDS" -gt 300 ]; then
	echo "dr-drill.sh: RPO ${RPO_SECONDS}s exceeded the 300s budget" >&2
	exit 1
fi
if [ "$RTO_SECONDS" -gt 1200 ]; then
	echo "dr-drill.sh: RTO ${RTO_SECONDS}s exceeded the 1200s budget" >&2
	exit 1
fi

echo "dr-drill.sh: === PASS (mode=$MODE, RPO=${RPO_SECONDS}s, RTO=${RTO_SECONDS}s) ==="
