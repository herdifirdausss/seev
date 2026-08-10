#!/usr/bin/env bash
# Compatibility aggregator — sources the focused library files in scripts/lib/
# and retains any implementation not yet extracted (P1.5 incremental migration).
#
# Callers source this file unchanged. The split libraries can also be sourced
# individually by scripts that need only a subset of functionality.
#
# A caller sources this AFTER setting ROOT_DIR (cd there first) and BEFORE
# using any function below. Callers are expected to `trap cleanup EXIT`
# themselves right after sourcing.

# ─── Source focused libraries (P1.5) ─────────────────────────────────────────
# shellcheck source=scripts/lib/backup.sh
# (backup.sh is sourced by individual scripts that need pgBackRest helpers,
#  not here — it requires caller-provided IMAGE/REPO_HOST_PATH variables.)

# The library files below are sourced after the config block below so that
# the config defaults are available to any library that reads them at source
# time. Actual source directives are at the end of the config/workdir block.

# ─── Config (overridable by the caller before sourcing) ────────────────────

POSTGRES_CONTAINER_EXPLICIT="${POSTGRES_CONTAINER:+1}"
POSTGRES_CONTAINER_EXPLICIT="${POSTGRES_CONTAINER_EXPLICIT:-0}"
REDIS_CONTAINER_EXPLICIT="${REDIS_CONTAINER:+1}"
REDIS_CONTAINER_EXPLICIT="${REDIS_CONTAINER_EXPLICIT:-0}"
RABBITMQ_CONTAINER_EXPLICIT="${RABBITMQ_CONTAINER:+1}"
RABBITMQ_CONTAINER_EXPLICIT="${RABBITMQ_CONTAINER_EXPLICIT:-0}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-seev-postgres-1}"
REDIS_CONTAINER="${REDIS_CONTAINER:-seev-redis-1}"
RABBITMQ_CONTAINER="${RABBITMQ_CONTAINER:-seev-rabbitmq-1}"
DB_USER="${DB_USER:-seev}"
DB_NAME="${DB_NAME:-seev}"
LEDGER_DB_NAME="${LEDGER_DB_NAME:-seev_ledger}"
AUTH_DB_NAME="${AUTH_DB_NAME:-seev_auth}"
PAYIN_DB_NAME="${PAYIN_DB_NAME:-seev_payin}"
PAYOUT_DB_NAME="${PAYOUT_DB_NAME:-seev_payout}"
FRAUD_DB_NAME="${FRAUD_DB_NAME:-seev_fraud}"
GATEWAY_DB_NAME="${GATEWAY_DB_NAME:-seev_gateway}"
ADMINBFF_DB_NAME="${ADMINBFF_DB_NAME:-seev_adminbff}"
ASSURANCE_DB_NAME="${ASSURANCE_DB_NAME:-seev_assurance}"
VENDOR_DB_NAME="${VENDOR_DB_NAME:-seev_vendor}"
JWT_SECRET="${JWT_SECRET:-change-me-to-a-random-32-plus-character-secret}"
# docs/roadmap/archive/49 TM-07: every service now REFUSES to boot with an empty
# JWT_ISSUER (internal/platform/config validate()) — issuer validation used to be
# silently skippable by leaving this unset.
JWT_ISSUER="${JWT_ISSUER:-seev}"
# docs/roadmap/archive/49 K5: every gRPC server now REFUSES to boot with an empty
# token (internal/platform/transport/grpc.NewServer fails fast — the old no-op-when-empty
# behavior that silently accepted every call is gone), so this can no
# longer default to empty the way it used to across this whole harness.
INTERNAL_GRPC_TOKEN="${INTERNAL_GRPC_TOKEN:-change-me-to-a-random-32-plus-character-token}"
# docs/roadmap/archive/51 "A8 T2.5b" (the contract migration): auth-service now
# REFUSES to boot without both of these — auth_users.email/full_name and
# kyc_submissions.payload have no plaintext column left to fall back to,
# so this pair is no longer the "optional outside production" ring T2.2
# originally shipped. Fixed 32-byte test values, same convention as every
# other fixed secret in this file.
CRYPTOX_KEY_V1="${CRYPTOX_KEY_V1:-4444444444444444444444444444444444444444444444444444444444444444}"
CRYPTOX_LOOKUP_KEY="${CRYPTOX_LOOKUP_KEY:-5555555555555555555555555555555555555555555555555555555555555555}"
# docs/roadmap/archive/51 T3 (K7): ledger-service now REFUSES to boot without this
# (money-safety idempotency-key digest ring, required unconditionally,
# never optional the way the field-encryption/export/closure rings
# below are) — fixed 32-byte test value, this harness's own convention
# for every other fixed secret above.
LEDGER_IDEMPOTENCY_KEY_V1="${LEDGER_IDEMPOTENCY_KEY_V1:-1111111111111111111111111111111111111111111111111111111111111111}"
# docs/roadmap/archive/51 T4/T5 (K9/K10): optional — export/closure requests simply
# return 503 when unset, never a boot failure — but this harness's own
# privacy-e2e.sh needs both actually configured to exercise those flows.
EXPORT_KEK_V1="${EXPORT_KEK_V1:-2222222222222222222222222222222222222222222222222222222222222222}"
CLOSURE_KEK_V1="${CLOSURE_KEK_V1:-3333333333333333333333333333333333333333333333333333333333333333}"
# Plan 57 T2/T3: gateway now REFUSES to boot without a non-empty
# MERCHANT_API_KEY_PEPPER (services/gateway/internal/merchant.NewModule panics on an empty
# pepper — the same "money-safety secret, never optional" posture as
# LEDGER_IDEMPOTENCY_KEY_V1 above) and unconditionally builds a cryptox
# ring for webhook-secret encryption (T7), so CRYPTOX_KEY_V1/
# CRYPTOX_LOOKUP_KEY (already defined above for auth-service) must also
# reach gateway's own process now, not just auth's.
MERCHANT_API_KEY_PEPPER="${MERCHANT_API_KEY_PEPPER:-6666666666666666666666666666666666666666666666666666666666666666}"
# Plan 59 C3: gateway's notification module requires this HMAC key to
# fingerprint push-device tokens for deduplication (DevicesHandler returns 503
# NOTIFICATION_CHANNEL_UNAVAILABLE when it's absent — same boot-blocking
# posture as MERCHANT_API_KEY_PEPPER above for the merchant module).
NOTIFY_TOKEN_FINGERPRINT_KEY="${NOTIFY_TOKEN_FINGERPRINT_KEY:-7777777777777777777777777777777777777777777777777777777777777777}"
# docs/roadmap/archive/49 TM-11: the per-IP(+path) rate limiter now actually enforces
# (previously bypassed by keying on the ephemeral source port) — this
# harness legitimately fires many requests per minute from ONE machine
# across many scenarios/scripts sharing that one IP, which production's
# 10-per-minute default was never sized for. Individual scenarios that
# specifically test rate-limiter enforcement (e.g. chaos scenario 9) export
# a much lower override around just their own server startup.
RATE_LIMIT_REQUESTS="${RATE_LIMIT_REQUESTS:-500}"
RATE_LIMIT_PER="${RATE_LIMIT_PER:-1m}"
RATE_LIMIT_BURST="${RATE_LIMIT_BURST:-500}"
APP_PORT="${APP_PORT:-18080}"
INTERNAL_PORT="${INTERNAL_PORT:-18081}"
LEDGER_GRPC_PORT="${LEDGER_GRPC_PORT:-19091}"
LEDGER_APP_PORT="${LEDGER_APP_PORT:-18090}"
LEDGER_INTERNAL_PORT="${LEDGER_INTERNAL_PORT:-18091}"
AUTH_APP_PORT="${AUTH_APP_PORT:-18082}"
AUTH_INTERNAL_PORT="${AUTH_INTERNAL_PORT:-18083}"
PAYIN_GRPC_PORT="${PAYIN_GRPC_PORT:-19092}"
PAYIN_ADMIN_PORT="${PAYIN_ADMIN_PORT:-18092}"
PAYOUT_GRPC_PORT="${PAYOUT_GRPC_PORT:-19093}"
PAYOUT_ADMIN_PORT="${PAYOUT_ADMIN_PORT:-18093}"
# docs/roadmap/archive/45 Task T4: a second payout-service instance sharing the same
# Postgres/Redis as the primary (started via start_payout_service_replica,
# below) — used only by chaos-test.sh's distributed-breaker scenario to
# prove breaker state converges across two real replicas. Never started by
# start_services/start_server; every other script/scenario is unaffected.
PAYOUT2_GRPC_PORT="${PAYOUT2_GRPC_PORT:-19193}"
PAYOUT2_ADMIN_PORT="${PAYOUT2_ADMIN_PORT:-18193}"
FRAUD_GRPC_PORT="${FRAUD_GRPC_PORT:-19094}"
FRAUD_ADMIN_PORT="${FRAUD_ADMIN_PORT:-18094}"
ADMINBFF_PORT="${ADMINBFF_PORT:-18095}"
ASSURANCE_PORT="${ASSURANCE_PORT:-18096}"
VENDOR_GRPC_PORT="${VENDOR_GRPC_PORT:-19098}"
VENDOR_APP_PORT="${VENDOR_APP_PORT:-18098}"
# The callback signing secret is consumed by both the VendorService child
# process and scenario code in the parent shell. Initialize/export it here,
# not only inside start_vendor_service's subshell, so set -u scenarios can sign
# their first callback before any service restart.
VENDOR_MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"
export VENDOR_MOCKVENDOR_SECRET
MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-script-test-mockvendor2-secret-at-least-32-chars-long}"
export MOCKVENDOR2_SECRET
REDIS_HOST_PORT="${REDIS_HOST_PORT:-6380}"

# docs/roadmap/archive/44 K7: SEEV_WORK_DIR lets a caller (T4's scheduled workflow, one
# job running business-e2e then chaos-all in sequence) pin two EXACT
# directories under $RUNNER_TEMP instead of two unrelated mktemp paths — so
# a failure-only artifact upload step can name them directly. Local/default
# behavior is untouched: no SEEV_WORK_DIR means the existing mktemp path.
# Validated before use so cleanup()'s `rm -rf "$WORK_DIR"` can never reach
# outside a directory this script itself owns: refuses to reuse an
# already-populated directory (must be new/empty), and — only when
# $RUNNER_TEMP is actually set, i.e. we're really on an Actions runner —
# refuses a path that isn't underneath it (blocks an accidental "/" or a
# shared ancestor a caller passed by mistake from ever reaching `rm -rf`).
if [ -n "${SEEV_WORK_DIR:-}" ]; then
	if [ -e "$SEEV_WORK_DIR" ] && [ -n "$(ls -A "$SEEV_WORK_DIR" 2>/dev/null)" ]; then
		echo "lib.sh: SEEV_WORK_DIR '$SEEV_WORK_DIR' already exists and is not empty — refusing to reuse it" >&2
		exit 1
	fi
	if [ -n "${RUNNER_TEMP:-}" ]; then
		case "$SEEV_WORK_DIR" in
			"$RUNNER_TEMP"/*) ;;
			*)
				echo "lib.sh: SEEV_WORK_DIR '$SEEV_WORK_DIR' is not underneath \$RUNNER_TEMP ('$RUNNER_TEMP')" >&2
				exit 1
				;;
		esac
	fi
	mkdir -p "$SEEV_WORK_DIR"
	WORK_DIR="$SEEV_WORK_DIR"
else
	WORK_DIR="$(mktemp -d "/tmp/seev-${LIB_WORK_DIR_PREFIX:-run}.XXXXXX")"
fi
OBJECT_STORE_DIR="${OBJECT_STORE_DIR:-$WORK_DIR/encrypted-objects}"
GATEWAY_BIN="$WORK_DIR/gateway"
LEDGER_BIN="$WORK_DIR/ledger-service"
AUTH_BIN="$WORK_DIR/auth-service"
PAYIN_BIN="$WORK_DIR/payin-service"
PAYOUT_BIN="$WORK_DIR/payout-service"
FRAUD_BIN="$WORK_DIR/fraud-service"
ADMINBFF_BIN="$WORK_DIR/admin-bff-service"
ASSURANCE_BIN="$WORK_DIR/assurance-service"
VENDOR_BIN="$WORK_DIR/vendor-service"
GENTOKEN_BIN="$WORK_DIR/gentoken"
CERTGEN_BIN="$WORK_DIR/certgen"
CRYPTOX_FIXTURE_BIN="$WORK_DIR/cryptox-fixture"
# docs/roadmap/archive/49 K3: every service loads its own identity + the shared CA
# from one directory (tools/certgen's output layout) — generated fresh into
# this run's own WORK_DIR, never committed, never reused across runs.
CERT_DIR="$WORK_DIR/certs"
GATEWAY_LOG="$WORK_DIR/gateway.log"
LEDGER_LOG="$WORK_DIR/ledger-service.log"
AUTH_LOG="$WORK_DIR/auth-service.log"
PAYIN_LOG="$WORK_DIR/payin-service.log"
PAYOUT_LOG="$WORK_DIR/payout-service.log"
FRAUD_LOG="$WORK_DIR/fraud-service.log"
ADMINBFF_LOG="$WORK_DIR/admin-bff-service.log"
ASSURANCE_LOG="$WORK_DIR/assurance-service.log"
VENDOR_LOG="$WORK_DIR/vendor-service.log"
GATEWAY_PID_FILE="$WORK_DIR/gateway.pid"
LEDGER_PID_FILE="$WORK_DIR/ledger-service.pid"
AUTH_PID_FILE="$WORK_DIR/auth-service.pid"
PAYIN_PID_FILE="$WORK_DIR/payin-service.pid"
PAYOUT_PID_FILE="$WORK_DIR/payout-service.pid"
FRAUD_PID_FILE="$WORK_DIR/fraud-service.pid"
ADMINBFF_PID_FILE="$WORK_DIR/admin-bff-service.pid"
ASSURANCE_PID_FILE="$WORK_DIR/assurance-service.pid"
VENDOR_PID_FILE="$WORK_DIR/vendor-service.pid"
PAYOUT2_LOG="$WORK_DIR/payout-service-2.log"
PAYOUT2_PID_FILE="$WORK_DIR/payout-service-2.pid"

# Postgres port as seen from the HOST — auto-detected below rather than
# assumed, so this keeps working regardless of what POSTGRES_PORT (or its
# 5433 default — see docker-compose.yml's own comment) docker-compose
# actually mapped.
DB_HOST_PORT=""

# ─── Source focused libraries (P1.5) ─────────────────────────────────────────
# shellcheck source=scripts/lib/core.sh
source "${ROOT_DIR}/scripts/lib/core.sh"
# shellcheck source=scripts/lib/compose.sh
source "${ROOT_DIR}/scripts/lib/compose.sh"
# shellcheck source=scripts/lib/postgres.sh
source "${ROOT_DIR}/scripts/lib/postgres.sh"
# shellcheck source=scripts/lib/migrations.sh
source "${ROOT_DIR}/scripts/lib/migrations.sh"
# shellcheck source=scripts/lib/http.sh
source "${ROOT_DIR}/scripts/lib/http.sh"
# shellcheck source=scripts/lib/fixtures.sh
source "${ROOT_DIR}/scripts/lib/fixtures.sh"
# shellcheck source=scripts/lib/assertions.sh
source "${ROOT_DIR}/scripts/lib/assertions.sh"
# scripts/lib/services.sh and scripts/lib/certs.sh are empty stubs — not sourced here.

log "work dir: $WORK_DIR (binaries + *.log; KEEP_WORK_DIR=1 preserves it past exit for postmortem)"

# ─── Docker / Postgres bootstrap ────────────────────────────────────────────
# NOTE: resolve_compose_containers, wait_for_container_healthy,
# wait_for_container_restart are now in scripts/lib/compose.sh (sourced above).
# psql_exec, detect_db_port, wait_for_redis_reachable, ensure_service_dbs,
# ensure_app_role are now in scripts/lib/postgres.sh (sourced above).
# _apply_service_migrations, apply_migrations are in scripts/lib/migrations.sh.
# json_field, json_get, json_get_required, curl_internal are in lib/http.sh.
# Fixture/async helpers are in scripts/lib/fixtures.sh.
# Assertion helpers are in scripts/lib/assertions.sh.

# detect_db_port, psql_exec, wait_for_container_healthy, wait_for_redis_reachable,
# wait_for_container_restart → now in scripts/lib/postgres.sh and compose.sh (sourced above).

ensure_deps_up() {
	assert_test_harness_environment
	log "ensuring postgres/redis/rabbitmq are up..."
	docker compose up -d postgres redis rabbitmq >/dev/null 2>&1
	# Resolve the containers from the active Compose project. The full gate uses
	# COMPOSE_PROJECT_NAME=seev-verify, while local scripts normally use the
	# default project; fixed seev-* names are valid only for the latter.
	resolve_compose_containers
	wait_for_container_healthy "$POSTGRES_CONTAINER"
	wait_for_container_healthy "$REDIS_CONTAINER"
	wait_for_redis_reachable
	wait_for_container_healthy "$RABBITMQ_CONTAINER"
	ensure_service_dbs
	detect_db_port
	apply_migrations
}

# ensure_service_dbs, ensure_app_role → scripts/lib/postgres.sh (sourced above)
# _apply_service_migrations, apply_migrations → scripts/lib/migrations.sh (sourced above)

# ─── Server lifecycle ───────────────────────────────────────────────────────

build_server() {
	if [ -x "$GATEWAY_BIN" ] && [ -x "$LEDGER_BIN" ] && [ -x "$AUTH_BIN" ] &&
		[ -x "$PAYIN_BIN" ] && [ -x "$PAYOUT_BIN" ] && [ -x "$FRAUD_BIN" ] &&
		[ -x "$ADMINBFF_BIN" ] && [ -x "$ASSURANCE_BIN" ] && [ -x "$VENDOR_BIN" ] &&
		[ -x "$GENTOKEN_BIN" ] && [ -x "$CERTGEN_BIN" ] && [ -x "$CRYPTOX_FIXTURE_BIN" ]; then
		log "reusing binaries already built for this test run..."
		generate_certs
		return
	fi
	log "building gateway + ledger-service + auth-service + payin-service + payout-service + fraud-service + admin-bff-service + assurance-service + vendor-service + gentoken + certgen binaries..."
	go build -o "$GATEWAY_BIN" ./services/gateway/cmd/gateway
	go build -o "$LEDGER_BIN" ./services/ledger/cmd/ledger
	go build -o "$AUTH_BIN" ./services/auth/cmd/auth
	go build -o "$PAYIN_BIN" ./services/payin/cmd/payin
	go build -o "$PAYOUT_BIN" ./services/payout/cmd/payout
	go build -o "$FRAUD_BIN" ./services/fraud/cmd/fraud
	go build -o "$ADMINBFF_BIN" ./services/adminbff/cmd/adminbff
	go build -o "$ASSURANCE_BIN" ./services/assurance/cmd/assurance
	go build -o "$VENDOR_BIN" ./services/vendor-service/cmd/vendor
	go build -o "$GENTOKEN_BIN" ./tools/gentoken
	go build -o "$CERTGEN_BIN" ./tools/certgen
	go build -o "$CRYPTOX_FIXTURE_BIN" ./tools/cryptox-fixture
	generate_certs
}

# generate_certs (docs/roadmap/archive/49 K3) issues a fresh CA plus one leaf per
# known identity into $CERT_DIR, once per run — every start_*_service
# function below points TLS_CERT_DIR at this same directory. Idempotent
# within a run (certgen's own --force-less issue skips a still-fresh
# leaf), but $CERT_DIR itself lives under $WORK_DIR so a genuinely fresh
# CA is generated every run, never reused stale across runs.
generate_certs() {
	log "generating mTLS certificates (docs/roadmap/archive/49 K3) into $CERT_DIR..."
	"$CERTGEN_BIN" init-ca --out "$CERT_DIR"
	for service in gateway auth ledger payin payout fraud admin-bff assurance vendor dev-operator prometheus backup-agent; do
		"$CERTGEN_BIN" issue --service "$service" --out "$CERT_DIR"
	done
}

start_fraud_service() {
	log "starting fraud-service (grpc $FRAUD_GRPC_PORT / admin $FRAUD_ADMIN_PORT)..."
	(
		export APP_NAME=fraud-service
		export APP_PORT=$FRAUD_ADMIN_PORT
		export GRPC_PORT=$FRAUD_GRPC_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=fraud_app
		export POSTGRES_PASSWORD=fraud_app
		export POSTGRES_DB=$FRAUD_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export REDIS_ENABLED="${REDIS_ENABLED:-true}"
		export REDIS_ADDR=localhost:$REDIS_HOST_PORT
		export REDIS_DB=1
		export RABBITMQ_HOST=localhost
		export RABBITMQ_USERNAME=seev
		export RABBITMQ_PASSWORD=seev
		export RABBITMQ_EXCHANGE=ledger.events
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export SCREENING_MODE="${SCREENING_MODE:-off}"
		export SCREENING_AMOUNT_THRESHOLD="${SCREENING_AMOUNT_THRESHOLD:-0}"
		export SCREENING_VELOCITY_MAX_PER_HOUR="${SCREENING_VELOCITY_MAX_PER_HOUR:-0}"
		export LOG_FORMAT=json
		nohup "$FRAUD_BIN" >>"$FRAUD_LOG" 2>&1 &
		echo $! >"$FRAUD_PID_FILE"
	)
	wait_for_service_up fraud-service "https://localhost:$FRAUD_ADMIN_PORT/health" "$FRAUD_PID_FILE" "$FRAUD_LOG"
}

start_payout_service() {
	log "starting payout-service (grpc $PAYOUT_GRPC_PORT / admin $PAYOUT_ADMIN_PORT)..."
	(
		export APP_NAME=payout-service
		export APP_PORT=$PAYOUT_ADMIN_PORT
		export GRPC_PORT=$PAYOUT_GRPC_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=payout_app
		export POSTGRES_PASSWORD=payout_app
		export POSTGRES_DB=$PAYOUT_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export REDIS_ENABLED="${REDIS_ENABLED:-true}"
		export REDIS_ADDR=localhost:$REDIS_HOST_PORT
		export REDIS_DB=0
		export LEDGER_GRPC_ADDR=localhost:$LEDGER_GRPC_PORT
		export FRAUD_GRPC_ADDR=localhost:$FRAUD_GRPC_PORT
		export VENDOR_GRPC_ADDR=localhost:$VENDOR_GRPC_PORT
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export CRYPTOX_KEY_V1=$CRYPTOX_KEY_V1
		export VENDOR_SERVICE_ENABLED=true
		export VENDOR_MOCKVENDOR_ENABLED=false
		export MOCKVENDOR2_ENABLED=true
		export MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-script-test-mockvendor2-secret-at-least-32-chars-long}"
		# docs/roadmap/archive/45 Task T2/K3: off by default (matches production
		# BREAKER_DISTRIBUTED default false) — a scenario opts in by
		# exporting BREAKER_DISTRIBUTED=true before calling this.
		export BREAKER_DISTRIBUTED="${BREAKER_DISTRIBUTED:-false}"
		export LOG_FORMAT=json
		nohup "$PAYOUT_BIN" >>"$PAYOUT_LOG" 2>&1 &
		echo $! >"$PAYOUT_PID_FILE"
	)
	wait_for_service_up payout-service "https://localhost:$PAYOUT_ADMIN_PORT/health" "$PAYOUT_PID_FILE" "$PAYOUT_LOG"
}

# start_payout_service_replica starts a SECOND payout-service process (same
# binary, same Postgres DB and Redis instance as the primary) on the
# PAYOUT2_* ports — docs/roadmap/archive/45 Task T4's distributed-breaker-across-
# replicas scenario needs two real processes sharing one breaker backend,
# not two separately-provisioned stacks. Never called by start_services;
# only chaos-test.sh's own scenario reaches for this directly, and must
# pair it with kill_payout_replica_hard/stop_payout_replica for teardown.
start_payout_service_replica() {
	log "starting payout-service replica 2 (grpc $PAYOUT2_GRPC_PORT / admin $PAYOUT2_ADMIN_PORT)..."
	(
		export APP_NAME=payout-service
		export APP_PORT=$PAYOUT2_ADMIN_PORT
		export GRPC_PORT=$PAYOUT2_GRPC_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=payout_app
		export POSTGRES_PASSWORD=payout_app
		export POSTGRES_DB=$PAYOUT_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export REDIS_ENABLED="${REDIS_ENABLED:-true}"
		export REDIS_ADDR=localhost:$REDIS_HOST_PORT
		export REDIS_DB=0
		export LEDGER_GRPC_ADDR=localhost:$LEDGER_GRPC_PORT
		export FRAUD_GRPC_ADDR=localhost:$FRAUD_GRPC_PORT
		export VENDOR_GRPC_ADDR=localhost:$VENDOR_GRPC_PORT
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export CRYPTOX_KEY_V1=$CRYPTOX_KEY_V1
		export VENDOR_SERVICE_ENABLED=true
		export VENDOR_MOCKVENDOR_ENABLED=false
		export MOCKVENDOR2_ENABLED=true
		export MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-script-test-mockvendor2-secret-at-least-32-chars-long}"
		export BREAKER_DISTRIBUTED="${BREAKER_DISTRIBUTED:-false}"
		export LOG_FORMAT=json
		nohup "$PAYOUT_BIN" >>"$PAYOUT2_LOG" 2>&1 &
		echo $! >"$PAYOUT2_PID_FILE"
	)
	wait_for_service_up payout-service-2 "https://localhost:$PAYOUT2_ADMIN_PORT/health" "$PAYOUT2_PID_FILE" "$PAYOUT2_LOG"
}

kill_payout_replica_hard() {
	if [ -f "$PAYOUT2_PID_FILE" ]; then
		local pid
		pid="$(cat "$PAYOUT2_PID_FILE")"
		log "kill -9 payout-service replica 2 pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$PAYOUT2_PID_FILE"
	fi
}

stop_payout_replica() {
	if [ -f "$PAYOUT2_PID_FILE" ]; then
		local pid
		pid="$(cat "$PAYOUT2_PID_FILE")"
		kill -TERM "$pid" 2>/dev/null || true
		wait_for_pid_gone "$pid"
		rm -f "$PAYOUT2_PID_FILE"
	fi
}

start_vendor_service() {
	log "starting vendor-service (grpc $VENDOR_GRPC_PORT / http $VENDOR_APP_PORT)..."
	(
		export APP_NAME=vendor-service
		export APP_PORT=$VENDOR_APP_PORT
		export GRPC_PORT=$VENDOR_GRPC_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=vendor_app
		export POSTGRES_PASSWORD=vendor_app
		export POSTGRES_DB=$VENDOR_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export PAYIN_GRPC_ADDR=localhost:$PAYIN_GRPC_PORT
		export PAYOUT_GRPC_ADDR=localhost:$PAYOUT_GRPC_PORT
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export CRYPTOX_KEY_V1=$CRYPTOX_KEY_V1
		export VENDOR_CALLBACK_CIDRS=127.0.0.1/32,::1/128
		export VENDOR_MOCKVENDOR_ENABLED=true
		export VENDOR_MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"
		export MOCKVENDOR2_ENABLED=true
		export MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-script-test-mockvendor2-secret-at-least-32-chars-long}"
		export LOG_FORMAT=json
		nohup "$VENDOR_BIN" >>"$VENDOR_LOG" 2>&1 &
		echo $! >"$VENDOR_PID_FILE"
	)
	wait_for_service_up vendor-service "https://localhost:$VENDOR_APP_PORT/health" "$VENDOR_PID_FILE" "$VENDOR_LOG"
}

start_payin_service() {
	log "starting payin-service (grpc $PAYIN_GRPC_PORT / admin $PAYIN_ADMIN_PORT)..."
	(
		export APP_NAME=payin-service
		export APP_PORT=$PAYIN_ADMIN_PORT
		export GRPC_PORT=$PAYIN_GRPC_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=payin_app
		export POSTGRES_PASSWORD=payin_app
		export POSTGRES_DB=$PAYIN_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export REDIS_ENABLED="${REDIS_ENABLED:-true}"
		export REDIS_ADDR=localhost:$REDIS_HOST_PORT
		export REDIS_DB=0
		export LEDGER_GRPC_ADDR=localhost:$LEDGER_GRPC_PORT
		export FRAUD_GRPC_ADDR=localhost:$FRAUD_GRPC_PORT
		export VENDOR_GRPC_ADDR=localhost:$VENDOR_GRPC_PORT
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export CRYPTOX_KEY_V1=$CRYPTOX_KEY_V1
		export VENDOR_SERVICE_ENABLED=true
		export VENDOR_MOCKVENDOR_ENABLED=false
		export MOCKVENDOR2_ENABLED=true
		export MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-script-test-mockvendor2-secret-at-least-32-chars-long}"
		export LOG_FORMAT=json
		nohup "$PAYIN_BIN" >>"$PAYIN_LOG" 2>&1 &
		echo $! >"$PAYIN_PID_FILE"
	)
	wait_for_service_up payin-service "https://localhost:$PAYIN_ADMIN_PORT/health" "$PAYIN_PID_FILE" "$PAYIN_LOG"
}

# gen_token mints a JWT via tools/gentoken (see that package's own doc comment)
# — the single canonical implementation, no more hand-rolled heredocs.
# gentoken defaults kyc_level to 1 (docs/roadmap/archive/39 Task T6, gotcha #9) so
# every existing gen_token call site in smoke-test.sh/chaos-test.sh keeps
# posting to gated routes without any change — pass an explicit ttl+level
# ("1h" "0") only if a script specifically wants to exercise the KYC gate
# itself.
gen_token() {
	JWT_SECRET="$JWT_SECRET" JWT_ISSUER="$JWT_ISSUER" "$GENTOKEN_BIN" "$@"
}

start_ledger_service() {
	log "starting ledger-service (grpc $LEDGER_GRPC_PORT / http $LEDGER_APP_PORT / internal $LEDGER_INTERNAL_PORT)..."
	(
		export APP_NAME=ledger-service
		export APP_PORT=$LEDGER_APP_PORT
		export INTERNAL_APP_PORT=$LEDGER_INTERNAL_PORT
		export GRPC_PORT=$LEDGER_GRPC_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=ledger_app
		export POSTGRES_PASSWORD=ledger_app
		export POSTGRES_DB=$LEDGER_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export REDIS_ENABLED="${REDIS_ENABLED:-true}"
		export REDIS_ADDR=localhost:$REDIS_HOST_PORT
		export RABBITMQ_HOST=localhost
		export RABBITMQ_USERNAME=seev
		export RABBITMQ_PASSWORD=seev
		export RABBITMQ_EXCHANGE=ledger.events
		export FRAUD_GRPC_ADDR=localhost:$FRAUD_GRPC_PORT
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export CRYPTOX_KEY_V1=$CRYPTOX_KEY_V1
		export LEDGER_IDEMPOTENCY_KEY_V1=$LEDGER_IDEMPOTENCY_KEY_V1
		export LOG_FORMAT=json
		nohup "$LEDGER_BIN" >>"$LEDGER_LOG" 2>&1 &
		echo $! >"$LEDGER_PID_FILE"
	)
	wait_for_service_up ledger-service "https://localhost:$LEDGER_APP_PORT/health" "$LEDGER_PID_FILE" "$LEDGER_LOG"
}

start_gateway() {
	log "starting gateway (port $APP_PORT / internal $INTERNAL_PORT, db port $DB_HOST_PORT)..."
	(
		export APP_PORT=$APP_PORT
		export INTERNAL_APP_PORT=$INTERNAL_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		# docs/roadmap/archive/16 Task T3: the running app connects as the restricted
		# app_service role, never the schema owner ($DB_USER, used only for
		# migrations/assertions in this script) — same split as production.
		export POSTGRES_USER=gateway_app
		export POSTGRES_PASSWORD=gateway_app
		export POSTGRES_DB=$GATEWAY_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export REDIS_ENABLED="${REDIS_ENABLED:-true}"
		export REDIS_ADDR=localhost:$REDIS_HOST_PORT
		export RABBITMQ_HOST=localhost
		export RABBITMQ_USERNAME=seev
		export RABBITMQ_PASSWORD=seev
		export RABBITMQ_EXCHANGE=ledger.events
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export LEDGER_GRPC_ADDR=localhost:$LEDGER_GRPC_PORT
		export LEDGER_USER_API_URL=https://localhost:$LEDGER_APP_PORT
		export PAYIN_GRPC_ADDR=localhost:$PAYIN_GRPC_PORT
		export PAYOUT_GRPC_ADDR=localhost:$PAYOUT_GRPC_PORT
		# Plan 57 T2/T3/T7: gateway's own merchant module now requires both of
		# these unconditionally at boot (see MERCHANT_API_KEY_PEPPER's own
		# comment above) — found live while writing scripts/merchant-e2e.sh:
		# every script sourcing this file was booting gateway without them.
		export CRYPTOX_KEY_V1=$CRYPTOX_KEY_V1
		export CRYPTOX_LOOKUP_KEY=$CRYPTOX_LOOKUP_KEY
		export MERCHANT_API_KEY_PEPPER=$MERCHANT_API_KEY_PEPPER
		export NOTIFY_TOKEN_FINGERPRINT_KEY=$NOTIFY_TOKEN_FINGERPRINT_KEY
		export LOG_FORMAT=json
		# Vendor callbacks are served by vendor-service; gateway only owns the
		# public API and no longer registers a /webhooks route.
		nohup "$GATEWAY_BIN" >>"$GATEWAY_LOG" 2>&1 &
		echo $! >"$GATEWAY_PID_FILE"
	)
	wait_for_service_up gateway "http://localhost:$APP_PORT/health" "$GATEWAY_PID_FILE" "$GATEWAY_LOG"
}

start_auth_service() {
	log "starting auth-service (http $AUTH_APP_PORT / internal $AUTH_INTERNAL_PORT)..."
	mkdir -p "$OBJECT_STORE_DIR"
	(
		export APP_NAME=auth-service
		export APP_PORT=$AUTH_APP_PORT
		export INTERNAL_APP_PORT=$AUTH_INTERNAL_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=auth_app
		export POSTGRES_PASSWORD=auth_app
		export POSTGRES_DB=$AUTH_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export REDIS_ENABLED="${REDIS_ENABLED:-true}"
		export REDIS_ADDR=localhost:$REDIS_HOST_PORT
		export LEDGER_GRPC_ADDR=localhost:$LEDGER_GRPC_PORT
		# docs/roadmap/archive/51 T5 (K10): where the closure saga calls ledger's
		# ClosureRouter — the internal (:$LEDGER_INTERNAL_PORT-class) port.
		export LEDGER_INTERNAL_API_URL=https://localhost:$LEDGER_INTERNAL_PORT
		export PAYIN_INTERNAL_API_URL=https://localhost:$PAYIN_ADMIN_PORT
		export PAYOUT_INTERNAL_API_URL=https://localhost:$PAYOUT_ADMIN_PORT
		export FRAUD_INTERNAL_API_URL=https://localhost:$FRAUD_ADMIN_PORT
		export GATEWAY_INTERNAL_API_URL=https://localhost:$INTERNAL_PORT
		export FRAUD_GRPC_ADDR=localhost:$FRAUD_GRPC_PORT
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export CRYPTOX_KEY_V1=$CRYPTOX_KEY_V1
		export CRYPTOX_LOOKUP_KEY=$CRYPTOX_LOOKUP_KEY
		export EXPORT_KEK_V1=$EXPORT_KEK_V1
		export CLOSURE_KEK_V1=$CLOSURE_KEK_V1
		export OBJECT_STORE_DIR=$OBJECT_STORE_DIR
		export LOG_FORMAT=json
		nohup "$AUTH_BIN" >>"$AUTH_LOG" 2>&1 &
		echo $! >"$AUTH_PID_FILE"
	)
	wait_for_service_up auth-service "https://localhost:$AUTH_INTERNAL_PORT/health" "$AUTH_PID_FILE" "$AUTH_LOG"
}

start_services() {
	start_ledger_service
	start_fraud_service
	start_auth_service
	start_payin_service
	start_payout_service
	start_vendor_service
	start_gateway
	start_assurance_service
}

start_assurance_service() {
	log "starting assurance-service (http $ASSURANCE_PORT)..."
	(
		export APP_NAME=assurance-service
		export APP_PORT=$ASSURANCE_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=assurance_app
		export POSTGRES_PASSWORD=assurance_app
		export POSTGRES_DB=$ASSURANCE_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export PAYIN_GRPC_ADDR=localhost:$PAYIN_GRPC_PORT
		export PAYOUT_GRPC_ADDR=localhost:$PAYOUT_GRPC_PORT
		export LEDGER_GRPC_ADDR=localhost:$LEDGER_GRPC_PORT
		# Assurance's FX reconciliation reader uses Ledger's internal HTTPS
		# listener, not the public user API. Keep the drill/capability port
		# override wired through; the default in config is only for a manually
		# started dev process on :8091.
		export LEDGER_INTERNAL_API_URL=https://localhost:$LEDGER_INTERNAL_PORT
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export TLS_CERT_DIR=$CERT_DIR
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export ASSURANCE_INTERVAL="${ASSURANCE_INTERVAL:-60s}"
		export ASSURANCE_CONSISTENCY_DELAY="${ASSURANCE_CONSISTENCY_DELAY:-2m}"
		export LOG_FORMAT=json
		nohup "$ASSURANCE_BIN" >>"$ASSURANCE_LOG" 2>&1 &
		echo $! >"$ASSURANCE_PID_FILE"
	)
	wait_for_service_up assurance-service "https://localhost:$ASSURANCE_PORT/health" "$ASSURANCE_PID_FILE" "$ASSURANCE_LOG"
}

start_adminbff_service() {
	log "starting admin-bff-service (http $ADMINBFF_PORT)..."
	(
		export APP_NAME=admin-bff-service
		export APP_PORT=$ADMINBFF_PORT
		export POSTGRES_HOST=localhost
		export POSTGRES_PORT=$DB_HOST_PORT
		export POSTGRES_USER=adminbff_app
		export POSTGRES_PASSWORD=adminbff_app
		export POSTGRES_DB=$ADMINBFF_DB_NAME
		export POSTGRES_SSL_MODE=disable
		export CRYPTOX_KEY_V1=$CRYPTOX_KEY_V1
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		# AUTH_SERVICE_URL targets auth's PUBLIC login endpoint and stays
		# plain (docs/roadmap/archive/49 anti-scope edge exception); every other
		# downstream target here is genuinely internal and flips to https.
		export AUTH_SERVICE_URL="${AUTH_SERVICE_URL:-http://localhost:$AUTH_APP_PORT}"
		export AUTH_ADMIN_SERVICE_URL="${AUTH_ADMIN_SERVICE_URL:-https://localhost:$AUTH_INTERNAL_PORT}"
		export LEDGER_SERVICE_URL="${LEDGER_SERVICE_URL:-https://localhost:$LEDGER_INTERNAL_PORT}"
		export PAYIN_SERVICE_URL="${PAYIN_SERVICE_URL:-https://localhost:$PAYIN_ADMIN_PORT}"
		export PAYOUT_SERVICE_URL="${PAYOUT_SERVICE_URL:-https://localhost:$PAYOUT_ADMIN_PORT}"
		export FRAUD_SERVICE_URL="${FRAUD_SERVICE_URL:-https://localhost:$FRAUD_ADMIN_PORT}"
		export GATEWAY_SERVICE_URL="${GATEWAY_SERVICE_URL:-https://localhost:$INTERNAL_PORT}"
		export ADMIN_BFF_SECURE_COOKIE="${ADMIN_BFF_SECURE_COOKIE:-false}"
		export LOG_FORMAT=json
		nohup "$ADMINBFF_BIN" >>"$ADMINBFF_LOG" 2>&1 &
		echo $! >"$ADMINBFF_PID_FILE"
	)
	wait_for_service_up admin-bff-service "https://localhost:$ADMINBFF_PORT/health" "$ADMINBFF_PID_FILE" "$ADMINBFF_LOG"
}

# curl_internal → scripts/lib/http.sh (sourced above)

wait_for_service_up() {
	local name=$1 url=$2 pid_file=$3 log_file=$4
	local tries=30
	local check=curl
	case "$url" in
		https://*) check=curl_internal ;;
	esac
	while [ "$tries" -gt 0 ]; do
		if "$check" -s -o /dev/null "$url"; then
			log "$name is up (pid $(cat "$pid_file" 2>/dev/null))"
			return 0
		fi
		sleep 1
		tries=$((tries - 1))
	done
	fail "$name did not come up in time — see $log_file"
	tail -40 "$log_file" || true
	return 1
}

# Compatibility for chaos-test while it is migrated to service-specific
# lifecycle controls in its own extraction phase.
start_server() { start_services; }

# wait_for_pid_gone polls kill -0 until the pid is actually gone (or ~10s
# elapses). Every service is started via `nohup bin & echo $! >pidfile` inside
# a "( ... )" subshell (see start_*_service below), so by the time the pid
# reaches a stop/kill function it is NOT a job of the calling shell — bash's
# builtin `wait PID` only blocks for actual children of the current shell, so
# the old `wait "$pid" 2>/dev/null || true` on that pid was a silent no-op:
# it returned instantly whether or not the process had actually exited. That
# made every
# graceful stop_services() call effectively async: the function returned
# immediately after sending SIGTERM while the real process (mid 30s graceful
# shutdown drain) was still bound to its port. The very next scenario's
# start_services() in a `chaos-test.sh all` run could then race a still-dying
# predecessor for the same ports (bind: address already in use), and
# wait_for_service_up would sometimes report a stale survivor as "up" — the
# same false-positive-health-check failure mode as the stop_server_gracefully
# bug above, just from a different mechanism. Real bug found reproducing
# docs/roadmap/archive/34 T2 scenario 5/6/7 failures that only appeared inside `all`,
# never in isolation.
wait_for_pid_gone() {
	local pid=$1 tries=100
	while [ "$tries" -gt 0 ] && kill -0 "$pid" 2>/dev/null; do
		sleep 0.1
		tries=$((tries - 1))
	done
	# Graceful shutdown can legitimately take up to the app's own 30s drain
	# timeout (server: shutting down .. timeout=30s) under load — 10s isn't
	# always enough. Never return with the pid (and therefore its port)
	# still alive: escalate to SIGKILL and poll a little longer so the very
	# next scenario's start_services() never races a still-dying predecessor.
	if kill -0 "$pid" 2>/dev/null; then
		kill -9 "$pid" 2>/dev/null || true
		tries=50
		while [ "$tries" -gt 0 ] && kill -0 "$pid" 2>/dev/null; do
			sleep 0.1
			tries=$((tries - 1))
		done
	fi
}

stop_gateway_only() {
	if [ -f "$GATEWAY_PID_FILE" ]; then
		local pid
		pid="$(cat "$GATEWAY_PID_FILE")"
		kill -TERM "$pid" 2>/dev/null || true
		wait_for_pid_gone "$pid"
		rm -f "$GATEWAY_PID_FILE"
	fi
}

# Compatibility alias: every caller of "stop the server gracefully" means
# "shut the whole stack down cleanly", same as start_server above means
# "start all eight" — a caller that stopped only the gateway here would leak
# the other seven processes, which then squat on the next scenario's ports
# and get silently misreported as "up" by wait_for_service_up (a stale
# survivor answering the same health-check URL). Real bug found running
# `chaos-test.sh all` end to end for docs/roadmap/archive/34 T2: five scenarios called
# this expecting a full stop and got only gateway killed.
stop_server_gracefully() { stop_services; }

stop_services() {
	stop_gateway_only
	if [ -f "$AUTH_PID_FILE" ]; then
		local auth_pid
		auth_pid="$(cat "$AUTH_PID_FILE")"
		kill -TERM "$auth_pid" 2>/dev/null || true
		wait_for_pid_gone "$auth_pid"
		rm -f "$AUTH_PID_FILE"
	fi
	if [ -f "$LEDGER_PID_FILE" ]; then
		local pid
		pid="$(cat "$LEDGER_PID_FILE")"
		kill -TERM "$pid" 2>/dev/null || true
		wait_for_pid_gone "$pid"
		rm -f "$LEDGER_PID_FILE"
	fi
	if [ -f "$PAYIN_PID_FILE" ]; then
		local payin_pid
		payin_pid="$(cat "$PAYIN_PID_FILE")"
		kill -TERM "$payin_pid" 2>/dev/null || true
		wait_for_pid_gone "$payin_pid"
		rm -f "$PAYIN_PID_FILE"
	fi
	if [ -f "$PAYOUT_PID_FILE" ]; then
		local payout_pid
		payout_pid="$(cat "$PAYOUT_PID_FILE")"
		kill -TERM "$payout_pid" 2>/dev/null || true
		wait_for_pid_gone "$payout_pid"
		rm -f "$PAYOUT_PID_FILE"
	fi
	if [ -f "$VENDOR_PID_FILE" ]; then
		local vendor_pid
		vendor_pid="$(cat "$VENDOR_PID_FILE")"
		kill -TERM "$vendor_pid" 2>/dev/null || true
		wait_for_pid_gone "$vendor_pid"
		rm -f "$VENDOR_PID_FILE"
	fi
	if [ -f "$FRAUD_PID_FILE" ]; then
		local fraud_pid
		fraud_pid="$(cat "$FRAUD_PID_FILE")"
		kill -TERM "$fraud_pid" 2>/dev/null || true
		wait_for_pid_gone "$fraud_pid"
		rm -f "$FRAUD_PID_FILE"
	fi
	if [ -f "$ADMINBFF_PID_FILE" ]; then
		local adminbff_pid
		adminbff_pid="$(cat "$ADMINBFF_PID_FILE")"
		kill -TERM "$adminbff_pid" 2>/dev/null || true
		wait_for_pid_gone "$adminbff_pid"
		rm -f "$ADMINBFF_PID_FILE"
	fi
	if [ -f "$ASSURANCE_PID_FILE" ]; then
		local assurance_pid
		assurance_pid="$(cat "$ASSURANCE_PID_FILE")"
		kill -TERM "$assurance_pid" 2>/dev/null || true
		wait_for_pid_gone "$assurance_pid"
		rm -f "$ASSURANCE_PID_FILE"
	fi
}

kill_server_hard() {
	if [ -f "$GATEWAY_PID_FILE" ]; then
		local pid
		pid="$(cat "$GATEWAY_PID_FILE")"
		log "kill -9 gateway pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$GATEWAY_PID_FILE"
	fi
}

kill_payin_hard() {
	if [ -f "$PAYIN_PID_FILE" ]; then
		local pid
		pid="$(cat "$PAYIN_PID_FILE")"
		log "kill -9 payin-service pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$PAYIN_PID_FILE"
	fi
}

kill_vendor_hard() {
	if [ -f "$VENDOR_PID_FILE" ]; then
		local pid
		pid="$(cat "$VENDOR_PID_FILE")"
		log "kill -9 vendor-service pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$VENDOR_PID_FILE"
	fi
}

kill_payout_hard() {
	if [ -f "$PAYOUT_PID_FILE" ]; then
		local pid
		pid="$(cat "$PAYOUT_PID_FILE")"
		log "kill -9 payout-service pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$PAYOUT_PID_FILE"
	fi
}

kill_ledger_hard() {
	if [ -f "$LEDGER_PID_FILE" ]; then
		local pid
		pid="$(cat "$LEDGER_PID_FILE")"
		log "kill -9 ledger-service pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$LEDGER_PID_FILE"
	fi
}

kill_fraud_hard() {
	if [ -f "$FRAUD_PID_FILE" ]; then
		local pid
		pid="$(cat "$FRAUD_PID_FILE")"
		log "kill -9 fraud-service pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$FRAUD_PID_FILE"
	fi
}

kill_auth_hard() {
	if [ -f "$AUTH_PID_FILE" ]; then
		local pid
		pid="$(cat "$AUTH_PID_FILE")"
		log "kill -9 auth-service pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$AUTH_PID_FILE"
	fi
}

kill_assurance_hard() {
	if [ -f "$ASSURANCE_PID_FILE" ]; then
		local pid
		pid="$(cat "$ASSURANCE_PID_FILE")"
		log "kill -9 assurance-service pid $pid"
		kill -9 "$pid" 2>/dev/null || true
		rm -f "$ASSURANCE_PID_FILE"
	fi
}

# ─── Test data helpers ───────────────────────────────────────────────────────

provision_user() {
	local user_id=$1
	psql_exec "$LEDGER_DB_NAME" -c "
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, status, created_by)
		VALUES (gen_random_uuid(), '$user_id', 'user', 'cash', 'IDR', 'active', 'script_test')
		ON CONFLICT DO NOTHING;" >/dev/null
	psql_exec "$LEDGER_DB_NAME" -c "
		INSERT INTO account_balances (account_id)
		SELECT id FROM accounts WHERE owner_id = '$user_id' AND type = 'cash'
		ON CONFLICT DO NOTHING;" >/dev/null
	# Keep the SQL fixture aligned with the gen_token fixture contract: account
	# provisioning also creates the fail-closed execution-time auth projection,
	# and gen_token's default claim is KYC level 1.
	psql_exec "$LEDGER_DB_NAME" -c "
		INSERT INTO money_movement_execution_subjects
			(user_id, status, kyc_level, tenant_status)
		VALUES ('$user_id', 'active', 1, 'active')
		ON CONFLICT (user_id, tenant_id) DO UPDATE SET
			status = 'active', kyc_level = 1, tenant_status = 'active', updated_at = now();" >/dev/null
	psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM accounts WHERE owner_id = '$user_id' AND type = 'cash' LIMIT 1;"
}

# provision_hold_account / fund_user / cash_account_id / account_balance → scripts/lib/fixtures.sh (sourced above)
# json_field / json_get / json_get_required / assert_http_status → scripts/lib/http.sh (sourced above)
# wait_for_payout_status / wait_for_vendor_call / wait_for_vendor_command_status → scripts/lib/fixtures.sh (sourced above)
# kyc_approve_l1 / kyc_submit_l2_and_admin_approve / await_notification / await_log_line → scripts/lib/fixtures.sh (sourced above)
# assert_metric_value / assert_ledger_balanced / assert_no_inconsistent_projections / assert_no_stuck_pending_transactions → scripts/lib/assertions.sh (sourced above)

# cleanup always stops every service (never leak a process past the
# script's own exit), but only deletes WORK_DIR (binaries + *.log) when
# KEEP_WORK_DIR is unset — set `KEEP_WORK_DIR=1` when re-running a single
# scenario for debugging so the *.log files under WORK_DIR survive for
# postmortem instead of being wiped on every exit, success or failure.
cleanup() {
	local exit_code=$?
	# Cleanup must never mask the assertion/bootstrap failure that triggered the
	# EXIT trap. This is especially important for set -e scripts: a failed
	# Docker bootstrap used to be followed by a successful rm, making the whole
	# smoke/business/admin command appear green.
	stop_services || true
	kill_payout_replica_hard || true
	if [ "${KEEP_WORK_DIR:-0}" = "1" ]; then
		log "KEEP_WORK_DIR=1 — leaving $WORK_DIR in place (binaries + *.log) for inspection"
	else
		rm -rf "$WORK_DIR" || true
	fi
	return "$exit_code"
}
