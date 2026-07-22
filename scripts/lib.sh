#!/usr/bin/env bash
# Shared bootstrap/assertion library for this repo's Bash test scripts
# (scripts/chaos-test.sh, scripts/smoke-test.sh). Sourced, never executed
# directly — every function here builds/starts a real server against a
# real docker-compose Postgres/Redis/RabbitMQ stack, so both scripts get
# identical, single-source-of-truth setup instead of drifting copies.
#
# A caller sources this AFTER setting ROOT_DIR (cd there first) and BEFORE
# using any function below. Callers are expected to `trap cleanup EXIT`
# themselves right after sourcing, so each script controls its own
# lifecycle (the library doesn't install the trap for you).

# ─── Config (overridable by the caller before sourcing) ────────────────────

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
JWT_SECRET="${JWT_SECRET:-change-me-to-a-random-32-plus-character-secret}"
# docs/plan/49 TM-07: every service now REFUSES to boot with an empty
# JWT_ISSUER (internal/config validate()) — issuer validation used to be
# silently skippable by leaving this unset.
JWT_ISSUER="${JWT_ISSUER:-seev}"
# docs/plan/49 K5: every gRPC server now REFUSES to boot with an empty
# token (pkg/grpcx.NewServer fails fast — the old no-op-when-empty
# behavior that silently accepted every call is gone), so this can no
# longer default to empty the way it used to across this whole harness.
INTERNAL_GRPC_TOKEN="${INTERNAL_GRPC_TOKEN:-change-me-to-a-random-32-plus-character-token}"
# docs/plan/49 TM-11: the per-IP(+path) rate limiter now actually enforces
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
# docs/plan/45 Task T4: a second payout-service instance sharing the same
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
REDIS_HOST_PORT="${REDIS_HOST_PORT:-6380}"

# docs/plan/44 K7: SEEV_WORK_DIR lets a caller (T4's scheduled workflow, one
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
GATEWAY_BIN="$WORK_DIR/gateway"
LEDGER_BIN="$WORK_DIR/ledger-service"
AUTH_BIN="$WORK_DIR/auth-service"
PAYIN_BIN="$WORK_DIR/payin-service"
PAYOUT_BIN="$WORK_DIR/payout-service"
FRAUD_BIN="$WORK_DIR/fraud-service"
ADMINBFF_BIN="$WORK_DIR/admin-bff-service"
ASSURANCE_BIN="$WORK_DIR/assurance-service"
GENTOKEN_BIN="$WORK_DIR/gentoken"
CERTGEN_BIN="$WORK_DIR/certgen"
# docs/plan/49 K3: every service loads its own identity + the shared CA
# from one directory (cmd/certgen's output layout) — generated fresh into
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
GATEWAY_PID_FILE="$WORK_DIR/gateway.pid"
LEDGER_PID_FILE="$WORK_DIR/ledger-service.pid"
AUTH_PID_FILE="$WORK_DIR/auth-service.pid"
PAYIN_PID_FILE="$WORK_DIR/payin-service.pid"
PAYOUT_PID_FILE="$WORK_DIR/payout-service.pid"
FRAUD_PID_FILE="$WORK_DIR/fraud-service.pid"
ADMINBFF_PID_FILE="$WORK_DIR/admin-bff-service.pid"
ASSURANCE_PID_FILE="$WORK_DIR/assurance-service.pid"
PAYOUT2_LOG="$WORK_DIR/payout-service-2.log"
PAYOUT2_PID_FILE="$WORK_DIR/payout-service-2.pid"

# Postgres port as seen from the HOST — auto-detected below rather than
# assumed, so this keeps working regardless of what POSTGRES_PORT (or its
# 5433 default — see docker-compose.yml's own comment) docker-compose
# actually mapped.
DB_HOST_PORT=""

log()  { printf '\033[1;34m[%s]\033[0m %s\n' "${LIB_LOG_TAG:-lib}" "$*"; }
ok()   { printf '\033[1;32m[ pass]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[ FAIL]\033[0m %s\n' "$*"; FAILED=1; }

FAILED=0

log "work dir: $WORK_DIR (binaries + *.log; KEEP_WORK_DIR=1 preserves it past exit for postmortem)"

# ─── Docker / Postgres bootstrap ────────────────────────────────────────────

detect_db_port() {
	DB_HOST_PORT="$(docker port "$POSTGRES_CONTAINER" 5432/tcp 2>/dev/null | head -1 | cut -d: -f2)"
	if [ -z "$DB_HOST_PORT" ]; then
		fail "could not detect host port for $POSTGRES_CONTAINER — is docker-compose up?"
		exit 1
	fi
	log "detected Postgres host port: $DB_HOST_PORT"
}

psql_exec() {
	local database="$DB_NAME"
	if [ "$#" -gt 0 ] && [[ "$1" != -* ]]; then
		database=$1
		shift
	fi
	docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$database" -v ON_ERROR_STOP=1 -t -A "$@"
}

wait_for_container_healthy() {
	local container=$1 tries=30
	while [ "$tries" -gt 0 ]; do
		local status
		status="$(docker inspect "$container" --format '{{.State.Health.Status}}' 2>/dev/null || echo "missing")"
		[ "$status" = "healthy" ] && return 0
		sleep 2
		tries=$((tries - 1))
	done
	fail "$container did not become healthy in time"
	return 1
}

ensure_deps_up() {
	log "ensuring postgres/redis/rabbitmq are up..."
	docker compose up -d postgres redis rabbitmq >/dev/null 2>&1
	wait_for_container_healthy "$POSTGRES_CONTAINER"
	wait_for_container_healthy "$REDIS_CONTAINER"
	wait_for_container_healthy "$RABBITMQ_CONTAINER"
	ensure_service_dbs
	detect_db_port
	apply_migrations
}

# ensure_service_dbs mirrors scripts/postgres-init/02-service-dbs.sh for
# existing Docker volumes, where entrypoint init scripts no longer run.
ensure_service_dbs() {
	local service database role exists
	for service in ledger auth payin payout fraud gateway adminbff assurance; do
		database="seev_${service}"
		role="${service}_app"
		exists="$(docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$database'")"
		if [ "$exists" != "1" ]; then
			docker exec -i "$POSTGRES_CONTAINER" createdb -U "$DB_USER" "$database"
		fi
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 <<-SQL >/dev/null
			DO \$\$
			BEGIN
			    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$role') THEN
			        EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', '$role', '$role');
			    END IF;
			END
			\$\$;
		SQL
	done
}

apply_migrations() {
	log "applying migrations (idempotent — CREATE TABLE IF... guards not present, so this is a no-op error we suppress if already applied)..."
	# Ledger must run first because migration 000009 creates the shared
	# app_service/app_readonly roles referenced by the other service SQL.
	for f in migrations/ledger/*.up.sql; do
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$LEDGER_DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
	done
	for f in migrations/auth/*.up.sql; do
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$AUTH_DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
	done
	for f in migrations/payin/*.up.sql; do
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$PAYIN_DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
	done
	for f in migrations/payout/*.up.sql; do
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$PAYOUT_DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
	done
	for f in migrations/fraud/*.up.sql; do
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$FRAUD_DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
	done
	for f in migrations/gateway/*.up.sql; do
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$GATEWAY_DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
	done
	for f in migrations/adminbff/*.up.sql; do
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$ADMINBFF_DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
	done
	for f in migrations/assurance/*.up.sql; do
		docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$ASSURANCE_DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
	done
	local service_dir
	for service_dir in migrations/*; do
		[ -d "$service_dir" ] || continue
		[ "$service_dir" = "migrations/ledger" ] && continue
		[ "$service_dir" = "migrations/auth" ] && continue
		[ "$service_dir" = "migrations/payin" ] && continue
		[ "$service_dir" = "migrations/payout" ] && continue
		[ "$service_dir" = "migrations/fraud" ] && continue
		[ "$service_dir" = "migrations/gateway" ] && continue
		[ "$service_dir" = "migrations/adminbff" ] && continue
		[ "$service_dir" = "migrations/assurance" ] && continue
		for f in "$service_dir"/*.up.sql; do
			[ -f "$f" ] || continue
			docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=0 <"$f" >/dev/null 2>&1 || true
		done
	done
	ensure_app_role "$DB_NAME" seev_app seev_app
	ensure_app_role "$LEDGER_DB_NAME" ledger_app ledger_app
	ensure_app_role "$AUTH_DB_NAME" auth_app auth_app
	ensure_app_role "$PAYIN_DB_NAME" payin_app payin_app
	ensure_app_role "$PAYOUT_DB_NAME" payout_app payout_app
	ensure_app_role "$FRAUD_DB_NAME" fraud_app fraud_app
	ensure_app_role "$GATEWAY_DB_NAME" gateway_app gateway_app
	ensure_app_role "$ADMINBFF_DB_NAME" adminbff_app adminbff_app
	ensure_app_role "$ASSURANCE_DB_NAME" assurance_app assurance_app
}

# ensure_app_role provisions the restricted login role the server actually
# connects as (docs/plan/16 Task T3) — the docker-compose postgres-init
# script only runs on a container's FIRST boot, which a script reusing an
# existing volume can't assume, so this is the idempotent belt-and-suspenders
# version run every time.
ensure_app_role() {
	local database=$1 role=$2 password=$3
	docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$database" -v ON_ERROR_STOP=1 <<-SQL >/dev/null
		DO \$\$
		BEGIN
		    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$role') THEN
		        CREATE ROLE $role LOGIN PASSWORD '$password';
		    END IF;
		END
		\$\$;
		GRANT app_service TO $role;
	SQL
}

# ─── Server lifecycle ───────────────────────────────────────────────────────

build_server() {
	log "building gateway + ledger-service + auth-service + payin-service + payout-service + fraud-service + admin-bff-service + assurance-service + gentoken + certgen binaries..."
	go build -o "$GATEWAY_BIN" ./cmd/gateway
	go build -o "$LEDGER_BIN" ./cmd/ledger-service
	go build -o "$AUTH_BIN" ./cmd/auth-service
	go build -o "$PAYIN_BIN" ./cmd/payin-service
	go build -o "$PAYOUT_BIN" ./cmd/payout-service
	go build -o "$FRAUD_BIN" ./cmd/fraud-service
	go build -o "$ADMINBFF_BIN" ./cmd/admin-bff-service
	go build -o "$ASSURANCE_BIN" ./cmd/assurance-service
	go build -o "$GENTOKEN_BIN" ./cmd/gentoken
	go build -o "$CERTGEN_BIN" ./cmd/certgen
	generate_certs
}

# generate_certs (docs/plan/49 K3) issues a fresh CA plus one leaf per
# known identity into $CERT_DIR, once per run — every start_*_service
# function below points TLS_CERT_DIR at this same directory. Idempotent
# within a run (certgen's own --force-less issue skips a still-fresh
# leaf), but $CERT_DIR itself lives under $WORK_DIR so a genuinely fresh
# CA is generated every run, never reused stale across runs.
generate_certs() {
	log "generating mTLS certificates (docs/plan/49 K3) into $CERT_DIR..."
	"$CERTGEN_BIN" init-ca --out "$CERT_DIR"
	for service in gateway auth ledger payin payout fraud admin-bff assurance dev-operator prometheus; do
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
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export VENDOR_MOCKVENDOR_ENABLED=true
		export VENDOR_MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"
		# mockvendor2 registered alongside mockvendor (docs/plan/40 Task T4) —
		# purely additive: only reachable once a routing rule actually points
		# at it (seeded by chaos-test.sh's vendor-failover scenario), every
		# other flow is unaffected.
		export MOCKVENDOR2_ENABLED=true
		export MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-script-test-mockvendor2-secret-at-least-32-chars-long}"
		# docs/plan/45 Task T2/K3: off by default (matches production
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
# PAYOUT2_* ports — docs/plan/45 Task T4's distributed-breaker-across-
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
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export VENDOR_MOCKVENDOR_ENABLED=true
		export VENDOR_MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"
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
		export LEDGER_GRPC_ADDR=localhost:$LEDGER_GRPC_PORT
		export FRAUD_GRPC_ADDR=localhost:$FRAUD_GRPC_PORT
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
		export VENDOR_MOCKVENDOR_ENABLED=true
		export VENDOR_MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"
		export MOCKVENDOR2_ENABLED=true
		export MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-script-test-mockvendor2-secret-at-least-32-chars-long}"
		export LOG_FORMAT=json
		nohup "$PAYIN_BIN" >>"$PAYIN_LOG" 2>&1 &
		echo $! >"$PAYIN_PID_FILE"
	)
	wait_for_service_up payin-service "https://localhost:$PAYIN_ADMIN_PORT/health" "$PAYIN_PID_FILE" "$PAYIN_LOG"
}

# gen_token mints a JWT via cmd/gentoken (see that package's own doc comment)
# — the single canonical implementation, no more hand-rolled heredocs.
# gentoken defaults kyc_level to 1 (docs/plan/39 Task T6, gotcha #9) so
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
		# docs/plan/16 Task T3: the running app connects as the restricted
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
		export LOG_FORMAT=json
		# mockvendor enabled unconditionally — purely additive: it only makes
		# /api/v1/payout and /webhooks/mockvendor reachable, flows that never
		# touch those routes are unaffected.
		export VENDOR_MOCKVENDOR_ENABLED=true
		export VENDOR_MOCKVENDOR_SECRET="${VENDOR_MOCKVENDOR_SECRET:-script-test-mockvendor-secret-at-least-32-chars-long}"
		nohup "$GATEWAY_BIN" >>"$GATEWAY_LOG" 2>&1 &
		echo $! >"$GATEWAY_PID_FILE"
	)
	wait_for_service_up gateway "http://localhost:$APP_PORT/health" "$GATEWAY_PID_FILE" "$GATEWAY_LOG"
}

start_auth_service() {
	log "starting auth-service (http $AUTH_APP_PORT / internal $AUTH_INTERNAL_PORT)..."
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
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		export INTERNAL_GRPC_TOKEN=$INTERNAL_GRPC_TOKEN
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
		export JWT_SECRET=$JWT_SECRET
		export JWT_ISSUER=$JWT_ISSUER
		export RATE_LIMIT_REQUESTS=$RATE_LIMIT_REQUESTS
		export RATE_LIMIT_PER=$RATE_LIMIT_PER
		export RATE_LIMIT_BURST=$RATE_LIMIT_BURST
		export TLS_CERT_DIR=$CERT_DIR
		# AUTH_SERVICE_URL targets auth's PUBLIC login endpoint and stays
		# plain (docs/plan/49 anti-scope edge exception); every other
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

# docs/plan/49 K6: every internal/admin listener now requires mTLS, so any
# curl targeting one must present the dev-operator client identity — the
# same identity a real operator's manual curl/browser session would use.
# A drop-in wrapper rather than touching every call site's URL string:
# rewrites http:// to https:// anywhere in the arguments before delegating
# to real curl, so callers only need their `curl` renamed to
# `curl_internal`, nothing else. Public endpoints (gateway/auth's edge)
# must never be routed through this — they stay on plain curl.
#
# -k/--insecure is required, not optional: this repo's certs carry ONLY a
# URI SAN (spiffe://seev/<service>, docs/plan/49 K3/K4), never a DNS SAN —
# pkg/tlsx's Go clients handle that via a custom VerifyConnection hook
# (InsecureSkipVerify + manual chain verification + URI SAN check), but
# curl exposes no equivalent "verify the chain, skip hostname matching"
# knob — only all-or-nothing. --cacert/--cert/--key above still present a
# real client identity and the SERVER still enforces its allowlist; this
# harness simply doesn't verify the server's identity back, which is an
# acceptable, deliberately scoped gap for local dev/test tooling — never
# how two Go services actually talk to each other in this repo.
curl_internal() {
	local args=() arg
	for arg in "$@"; do
		case "$arg" in
		http://*) args+=("https://${arg#http://}") ;;
		*) args+=("$arg") ;;
		esac
	done
	curl -k --cacert "$CERT_DIR/ca.pem" --cert "$CERT_DIR/dev-operator.pem" --key "$CERT_DIR/dev-operator-key.pem" "${args[@]}"
}

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
# docs/plan/34 T2 scenario 5/6/7 failures that only appeared inside `all`,
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
# "start all six" — a caller that stopped only the gateway here would leak
# the other five processes, which then squat on the next scenario's ports
# and get silently misreported as "up" by wait_for_service_up (a stale
# survivor answering the same health-check URL). Real bug found running
# `chaos-test.sh all` end to end for docs/plan/34 T2: five scenarios called
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
	psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM accounts WHERE owner_id = '$user_id' AND type = 'cash' LIMIT 1;"
}

# provision_hold_account additionally provisions the 'hold' account
# withdraw_initiate/withdraw_settle/withdraw_cancel (and payout, which posts
# those under the hood) require — provision_user alone (cash only) is not
# enough for any withdraw_* flow.
provision_hold_account() {
	local user_id=$1
	psql_exec "$LEDGER_DB_NAME" -c "
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, status, created_by)
		VALUES (gen_random_uuid(), '$user_id', 'user', 'hold', 'IDR', 'active', 'script_test')
		ON CONFLICT DO NOTHING;" >/dev/null
	psql_exec "$LEDGER_DB_NAME" -c "
		INSERT INTO account_balances (account_id)
		SELECT id FROM accounts WHERE owner_id = '$user_id' AND type = 'hold'
		ON CONFLICT DO NOTHING;" >/dev/null
}

# fund_user credits a fresh user's cash account via a real money_in
# transaction on the INTERNAL router (settlement[bca] -> user.cash), so the
# funding itself stays part of the double-entry ledger and doesn't trip
# v_account_balance_audit the way a raw `UPDATE account_balances` would.
# money_in credits whoever's JWT `sub` claim is on the request, NOT any
# target_user_id in the body (internal/ledger/processors/money_in.go) — so
# this mints a token for user_id itself, not the caller/service identity.
# Repeated runs against a reused development volume top up only the delta to
# the requested target. The delta uses its own idempotency key, so the helper
# remains safe if a process is interrupted after the ledger post.
fund_user() {
	local user_id=$1
	local amount=${2:-1000000}
	local cash current delta
	cash="$(cash_account_id "$user_id")"
	current="$(account_balance "$cash")"
	if ! [[ "$current" =~ ^[0-9]+$ ]]; then
		current=0
	fi
	delta=$((amount - current))
	if [ "$delta" -le 0 ]; then
		return 0
	fi
	local fund_token
	fund_token="$(gen_token "$user_id")"
	curl_internal -s -o /dev/null -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $fund_token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"fund-$user_id-$delta\",\"type\":\"money_in\",\"amount\":\"$delta\",\"metadata\":{\"gateway\":\"bca\"}}"
}

cash_account_id() {
	psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM accounts WHERE owner_id = '$1' AND type = 'cash' LIMIT 1;"
}

account_balance() {
	psql_exec "$LEDGER_DB_NAME" -c "SELECT balance FROM account_balances WHERE account_id = '$1';"
}

# ─── HTTP response helpers ───────────────────────────────────────────────────

# json_field extracts "key":"value" (string fields only) from a JSON blob
# read on stdin — good enough for this repo's flat response shapes, avoids
# a jq dependency. Shared by every script that curls the JSON API.
json_field() {
	sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"
}

# wait_for_payout_status polls payout_requests.status until it matches want
# or tries (default 40, 2s apart = 80s) are exhausted (docs/plan/45 Task T1,
# K1's own admitted API-behavior change): POST /api/v1/payout now returns
# right after hold+enqueue — the vendor result (settled/vendor_pending/
# cancelled/failed) is always driven asynchronously by the relay's own
# ~1s poll interval, even in what used to be called "instant-settle" mode.
# Every script that used to read the terminal status straight off the
# create response body must poll this instead. Shared by chaos-test.sh and
# business-e2e.sh — do not duplicate this loop in either script.
wait_for_payout_status() {
	local id=$1 want=$2 tries=${3:-40}
	local status=""
	while [ "$tries" -gt 0 ]; do
		status="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id';")"
		[ "$status" = "$want" ] && return 0
		sleep 2
		tries=$((tries - 1))
	done
	fail "payout $id did not reach status '$want' in time (last seen: '$status')"
	return 1
}

# wait_for_vendor_call polls payout_vendor_calls until at least one row with
# the given outcome ('accepted'|'rejected'|'uncertain') has been recorded
# for id, or tries (default 40, 1s apart) are exhausted. docs/plan/45 Task
# T1: dispatch is now async (the relay's own ~1s poll interval), so a test
# that needs to prove "the vendor was genuinely called and returned X"
# before taking its next step (e.g. rewriting a mock destination to let a
# retry succeed) must wait for this evidence instead of assuming the vendor
# call already happened synchronously inside an earlier curl.
wait_for_vendor_call() {
	local id=$1 outcome=$2 tries=${3:-40}
	local count="0"
	while [ "$tries" -gt 0 ]; do
		count="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_vendor_calls WHERE payout_request_id = '$id' AND outcome = '$outcome';")"
		[ "$count" != "0" ] && return 0
		sleep 1
		tries=$((tries - 1))
	done
	fail "payout $id never recorded a vendor call with outcome '$outcome' (last count: $count)"
	return 1
}

# wait_for_vendor_command_status polls the LIVE (pending/processing/failed)
# payout_vendor_commands row for a request until its status matches want, or
# tries (default 40, 1s apart) are exhausted. docs/plan/45 Task T1: used
# when a test needs to know a command has fully finished a dispatch attempt
# (i.e. FailCommand/CompleteCommand has actually committed) before mutating
# state the relay itself also touches — waiting on payout_vendor_calls alone
# (wait_for_vendor_call) only proves the audit write landed, which commits
# slightly BEFORE the command's own status transition in the same dispatch.
wait_for_vendor_command_status() {
	local id=$1 want=$2 tries=${3:-40}
	local status=""
	while [ "$tries" -gt 0 ]; do
		status="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_vendor_commands WHERE payout_request_id = '$id' AND status IN ('pending','processing','failed') LIMIT 1;")"
		[ "$status" = "$want" ] && return 0
		sleep 1
		tries=$((tries - 1))
	done
	fail "payout $id's live vendor command did not reach status '$want' in time (last seen: '$status')"
	return 1
}

# ─── KYC dance helpers (docs/plan/39 Task T6, gotcha #9 master) ─────────────
#
# Every script that transacts as a REAL registered user (not a gen_token
# fixture — see gen_token's own doc comment for why gentoken users don't
# need this) must clear the KYC gate before it can post to a gated route
# (POST /topup, POST /payout, POST /api/v1/ledger/transactions*), or every
# one of them now 403s KYC_REQUIRED. This is the ONE place that dance lives.

# kyc_approve_l1 submits an L1 KYC request against a REAL auth-service user
# (auth_port's public listener) — the mock provider auto-approves L1 when no
# mock_mode is given — then refreshes to obtain a NEW token pair carrying
# the updated kyc_level claim (the claim only refreshes on login/refresh,
# docs/plan/39 Task T4, so the caller's OLD access token stays stuck at
# whatever level it was minted with). Echoes the RAW refresh response JSON
# to stdout — refresh tokens rotate (single-use), so the caller MUST
# extract and keep the NEW refresh_token too (json_field refresh_token), or
# a second dance later (e.g. kyc_submit_l2_and_admin_approve) will fail
# replaying an already-revoked token.
kyc_approve_l1() {
	local auth_port=$1 access_token=$2 refresh_token=$3
	curl -s -o /dev/null -X POST "http://localhost:$auth_port/api/v1/users/me/kyc" \
		-H "Authorization: Bearer $access_token" -H "Content-Type: application/json" \
		-d '{"level_requested":1}'
	curl -s -X POST "http://localhost:$auth_port/api/v1/auth/refresh" \
		-H "Content-Type: application/json" -d "{\"refresh_token\":\"$refresh_token\"}"
}

# kyc_submit_l2_and_admin_approve submits L2 (the mock provider ALWAYS
# refers L2 to manual review, regardless of mock_mode — docs/plan/39's own
# locked decision), approves it with an admin token against auth's INTERNAL
# listener (auth_internal_port), then refreshes. Echoes the RAW refresh
# response JSON to stdout — same rotating-refresh-token caveat as
# kyc_approve_l1 above.
kyc_submit_l2_and_admin_approve() {
	local auth_port=$1 auth_internal_port=$2 admin_token=$3 access_token=$4 refresh_token=$5
	local submit_resp submission_id
	submit_resp="$(curl -s -X POST "http://localhost:$auth_port/api/v1/users/me/kyc" \
		-H "Authorization: Bearer $access_token" -H "Content-Type: application/json" \
		-d '{"level_requested":2}')"
	submission_id="$(echo "$submit_resp" | json_field id)"
	curl_internal -s -o /dev/null -X POST "http://localhost:$auth_internal_port/api/v1/admin/kyc/submissions/$submission_id/approve" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json"
	curl -s -X POST "http://localhost:$auth_port/api/v1/auth/refresh" \
		-H "Content-Type: application/json" -d "{\"refresh_token\":\"$refresh_token\"}"
}

# await_notification polls GET /api/v1/notifications (on the gateway,
# $APP_PORT) for token until a notification with the given title substring
# appears, or fails after ~10s — the outbox relay -> RabbitMQ ->
# internal/notify consumer path is asynchronous, so this can't be a single
# immediate curl+grep like most other assertions in these scripts.
await_notification() {
	local token=$1 title=$2 tries=20
	while [ "$tries" -gt 0 ]; do
		local resp
		resp="$(curl -s "http://localhost:$APP_PORT/api/v1/notifications" -H "Authorization: Bearer $token")"
		if echo "$resp" | grep -q "\"title\":\"$title\""; then
			ok "notification \"$title\" delivered"
			return 0
		fi
		sleep 0.5
		tries=$((tries - 1))
	done
	fail "notification \"$title\" never appeared within timeout"
	return 1
}

# await_log_line polls a log file for pattern until it appears, or fails
# after ~10s — same rationale as await_notification: any consumer fed via
# the outbox relay -> RabbitMQ hop (e.g. fraud-service's velocity consumer)
# processes asynchronously, so a single immediate grep can flake on a slow
# CI box even though the message eventually arrives.
await_log_line() {
	local logfile=$1 pattern=$2 description=$3 tries=20
	while [ "$tries" -gt 0 ]; do
		grep -q "$pattern" "$logfile" 2>/dev/null && { ok "$description"; return 0; }
		sleep 0.5
		tries=$((tries - 1))
	done
	fail "$description — pattern '$pattern' never appeared in $logfile within timeout"
	return 1
}

# ─── Prometheus metric assertions ───────────────────────────────────────────

# assert_metric_value curls a /metrics endpoint and asserts a Prometheus
# gauge/counter's CURRENT value for a line matching every given "label=value"
# substring (docs/plan/45 Task T4 — cache_redis_backend_active,
# vendorgw_breaker_backend). Label order in the exposition text is never
# assumed (client_golang doesn't guarantee it), so each label is checked as
# an independent substring on the already-narrowed-down line rather than a
# single fixed-order regex. Reports ok/fail itself (same convention as
# assert_ledger_balanced below) so callers just call it inline.
assert_metric_value() {
	local url=$1 metric=$2 expected=$3 desc=$4
	shift 4
	local matches="" label value
	local fetch=curl
	case "$url" in
	https://*) fetch=curl_internal ;;
	esac
	matches="$("$fetch" -s "$url" | grep "^${metric}{")"
	for label in "$@"; do
		matches="$(printf '%s\n' "$matches" | grep -F -- "$label")"
	done
	value="$(printf '%s\n' "$matches" | head -1 | awk '{print $2}')"
	if [ "$value" = "$expected" ]; then
		ok "$desc"
	else
		fail "$desc — got '$value', expected '$expected' (metric=$metric labels=[$*] url=$url)"
	fi
}

# ─── Ledger integrity assertions ────────────────────────────────────────────

# assert_ledger_balanced fails if fn_verify_ledger_balance() finds ANY
# unbalanced transaction across all of history — the single most important
# assertion in every script that uses this library.
assert_ledger_balanced() {
	local rows
	rows="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM fn_verify_ledger_balance('-infinity','infinity');" | tr -d '[:space:]')"
	if [ "$rows" = "0" ]; then
		ok "fn_verify_ledger_balance() found 0 unbalanced transactions"
	else
		fail "fn_verify_ledger_balance() found $rows unbalanced transaction(s) — MONEY LOST OR CREATED"
		psql_exec "$LEDGER_DB_NAME" -c "SELECT * FROM fn_verify_ledger_balance('-infinity','infinity');"
	fi
}

# assert_no_inconsistent_projections fails if any account's stored balance
# disagrees with the balance computed from its ledger_entries.
assert_no_inconsistent_projections() {
	local rows
	rows="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM v_account_balance_audit WHERE is_consistent = false;" | tr -d '[:space:]')"
	if [ "$rows" = "0" ]; then
		ok "v_account_balance_audit: all accounts consistent"
	else
		fail "v_account_balance_audit found $rows inconsistent account(s)"
		psql_exec "$LEDGER_DB_NAME" -c "SELECT * FROM v_account_balance_audit WHERE is_consistent = false;"
	fi
}

# assert_no_stuck_pending_transactions fails if any ledger_transactions row
# is still 'pending' — execTransfer always marks posted/failed in the same
# DB transaction as building the entries, so a lingering 'pending' row means
# a partial write escaped that guarantee.
assert_no_stuck_pending_transactions() {
	local rows
	rows="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM ledger_transactions WHERE status = 'pending';" | tr -d '[:space:]')"
	if [ "$rows" = "0" ]; then
		ok "no ledger_transactions stuck in 'pending' (no partial writes)"
	else
		fail "$rows ledger_transactions stuck in 'pending' — partial write detected"
		psql_exec "$LEDGER_DB_NAME" -c "SELECT id, type, status, created_at FROM ledger_transactions WHERE status = 'pending';"
	fi
}

# cleanup always stops every service (never leak a process past the
# script's own exit), but only deletes WORK_DIR (binaries + *.log) when
# KEEP_WORK_DIR is unset — set `KEEP_WORK_DIR=1` when re-running a single
# scenario for debugging so the *.log files under WORK_DIR survive for
# postmortem instead of being wiped on every exit, success or failure.
cleanup() {
	stop_services
	kill_payout_replica_hard
	if [ "${KEEP_WORK_DIR:-0}" = "1" ]; then
		log "KEEP_WORK_DIR=1 — leaving $WORK_DIR in place (binaries + *.log) for inspection"
	else
		rm -rf "$WORK_DIR"
	fi
}
