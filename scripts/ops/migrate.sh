#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

usage() {
	cat >&2 <<EOF
usage: $(basename "$0") [--help] [--dry-run] up|down

  up   Apply all pending migrations for SERVICE.
  down Roll back one migration step (requires SEEV_MIGRATE_DOWN_ACK).

Required environment variables:
  SERVICE                   One of: adminbff, assurance, auth, fraud, gateway,
                            ledger, payin, payout, vendor
  APP_ENV                   One of: development, test, ci, drill, load, gameday,
                            production
Options:
  --help, -h                Show this help and exit 0.
  --dry-run                 Print the action that would be taken without executing.

For rollback:
  SEEV_MIGRATE_DOWN_ACK=<env>:<service>
  SEEV_MIGRATE_DOWN_PRODUCTION_OVERRIDE=1  (additionally required for production)
EOF
}

DRY_RUN=0
COMMAND=""
for arg in "$@"; do
	case "$arg" in
		--help | -h)
			usage
			exit 0
			;;
		--dry-run) DRY_RUN=1 ;;
		up | down) COMMAND="$arg" ;;
		--*)
			echo "migrate: unknown option: $arg" >&2
			usage
			exit 2
			;;
		*)
			echo "migrate: unexpected argument: $arg" >&2
			usage
			exit 2
			;;
	esac
done

if [ -z "$COMMAND" ]; then
	echo "migrate: command (up|down) is required" >&2
	usage
	exit 2
fi

# SERVICE must always be set explicitly — no silent default (P1.2).
SERVICE="${SERVICE:-}"
if [ -z "$SERVICE" ]; then
	echo "migrate: SERVICE must be set explicitly (e.g. SERVICE=ledger)" >&2
	exit 2
fi
case "$SERVICE" in
	adminbff | assurance | auth | fraud | gateway | ledger | payin | payout | vendor) ;;
	*)
		echo "migrate: unsupported service: $SERVICE" >&2
		exit 2
		;;
esac

MIGRATE_USER="${POSTGRES_MIGRATE_USER:-seev}"
MIGRATE_PASSWORD="${POSTGRES_MIGRATE_PASSWORD:-seev}"
HOST="${POSTGRES_HOST:-localhost}"
PORT="${POSTGRES_PORT:-5433}"
SSL_MODE="${POSTGRES_SSL_MODE:-disable}"
APP_ENV="${APP_ENV:-development}"

[[ "$MIGRATE_USER" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || {
	echo "migrate: unsafe POSTGRES_MIGRATE_USER" >&2
	exit 2
}
[[ "$HOST" =~ ^[A-Za-z0-9._:-]+$ ]] || {
	echo "migrate: unsafe POSTGRES_HOST" >&2
	exit 2
}
[[ "$PORT" =~ ^[0-9]+$ && "$PORT" -ge 1 && "$PORT" -le 65535 ]] || {
	echo "migrate: POSTGRES_PORT must be between 1 and 65535" >&2
	exit 2
}
case "$SSL_MODE" in
	disable | allow | prefer | require | verify-ca | verify-full) ;;
	*)
		echo "migrate: unsupported POSTGRES_SSL_MODE: $SSL_MODE" >&2
		exit 2
		;;
esac
if [ "$DRY_RUN" -eq 0 ]; then
	command -v migrate >/dev/null 2>&1 || {
		echo "migrate: golang-migrate is required in PATH" >&2
		exit 2
	}
fi

# Keep the password in the child environment, not in the DSN/argv or Make's
# echoed recipe. lib/pq reads PGPASSWORD when the URL has no password field.
export PGPASSWORD="$MIGRATE_PASSWORD"
DSN="postgres://$MIGRATE_USER@$HOST:$PORT/seev_$SERVICE?sslmode=$SSL_MODE&x-migrations-table=schema_migrations_$SERVICE"

MIGRATION_DIR="$SERVICE"
[[ "$SERVICE" == vendor ]] && MIGRATION_DIR=vendor-service

if [[ "$COMMAND" == "up" ]]; then
	if [[ "$DRY_RUN" == "1" ]]; then
		printf '[DRY-RUN] would run: migrate -path services/%s/migrations -database <DSN> up\n' "$MIGRATION_DIR"
		exit 0
	fi
	exec migrate -path "services/$MIGRATION_DIR/migrations" -database "$DSN" up
fi

# ── 'down' rollback: explicit acknowledgement required (P1.2) ────────────────
#
# Operator contract:
#   APP_ENV=<env>  SERVICE=<service>  SEEV_MIGRATE_DOWN_ACK=<env>:<service>
#   ./scripts/migrate.sh down
#
# Production rollback additionally requires:
#   SEEV_MIGRATE_DOWN_PRODUCTION_OVERRIDE=1
#
# The script prints target host, database, service, and current version before
# executing. Credentials are never printed.

REQUIRED_ACK="${APP_ENV}:${SERVICE}"
PROVIDED_ACK="${SEEV_MIGRATE_DOWN_ACK:-}"

if [ -z "$PROVIDED_ACK" ]; then
	cat >&2 <<EOF
migrate: rollback requires explicit acknowledgement.

Set:
  SEEV_MIGRATE_DOWN_ACK=${REQUIRED_ACK}

Full example:
  APP_ENV=${APP_ENV} SERVICE=${SERVICE} SEEV_MIGRATE_DOWN_ACK=${REQUIRED_ACK} ./scripts/migrate.sh down

This prevents accidental rollback of the wrong environment or service.
EOF
	exit 2
fi

if [ "$PROVIDED_ACK" != "$REQUIRED_ACK" ]; then
	echo "migrate: acknowledgement mismatch — got '$PROVIDED_ACK', expected '$REQUIRED_ACK'" >&2
	exit 2
fi

if [ "$APP_ENV" = "production" ]; then
	PROD_OVERRIDE="${SEEV_MIGRATE_DOWN_PRODUCTION_OVERRIDE:-}"
	if [ "$PROD_OVERRIDE" != "1" ]; then
		echo "migrate: production rollback requires SEEV_MIGRATE_DOWN_PRODUCTION_OVERRIDE=1 in addition to the acknowledgement." >&2
		exit 2
	fi
	echo "migrate: WARNING — rolling back production database for service '$SERVICE'." >&2
fi

echo "migrate: rollback target:"
echo "  host:     $HOST:$PORT"
echo "  database: seev_${SERVICE}"
echo "  service:  $SERVICE"
echo "  env:      $APP_ENV"
echo "  action:   down 1 (one migration step)"

if [[ "$DRY_RUN" == "1" ]]; then
	printf '[DRY-RUN] would run: migrate -path services/%s/migrations -database <DSN> down 1\n' "$MIGRATION_DIR"
	exit 0
fi

exec migrate -path "services/$MIGRATION_DIR/migrations" -database "$DSN" down 1
