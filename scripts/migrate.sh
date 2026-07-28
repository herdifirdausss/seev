#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

COMMAND="${1:-}"
case "$COMMAND" in
	up|down) ;;
	*) echo "migrate: usage: $0 up|down" >&2; exit 2 ;;
esac

SERVICE="${SERVICE:-ledger}"
case "$SERVICE" in
	adminbff|assurance|auth|fraud|gateway|ledger|payin|payout|vendor) ;;
	*) echo "migrate: unsupported service: $SERVICE" >&2; exit 2 ;;
esac

MIGRATE_USER="${POSTGRES_MIGRATE_USER:-seev}"
MIGRATE_PASSWORD="${POSTGRES_MIGRATE_PASSWORD:-seev}"
HOST="${POSTGRES_HOST:-localhost}"
PORT="${POSTGRES_PORT:-5433}"
SSL_MODE="${POSTGRES_SSL_MODE:-disable}"

[[ "$MIGRATE_USER" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || { echo "migrate: unsafe POSTGRES_MIGRATE_USER" >&2; exit 2; }
[[ "$HOST" =~ ^[A-Za-z0-9._:-]+$ ]] || { echo "migrate: unsafe POSTGRES_HOST" >&2; exit 2; }
[[ "$PORT" =~ ^[0-9]+$ && "$PORT" -ge 1 && "$PORT" -le 65535 ]] || { echo "migrate: POSTGRES_PORT must be between 1 and 65535" >&2; exit 2; }
case "$SSL_MODE" in
	disable|allow|prefer|require|verify-ca|verify-full) ;;
	*) echo "migrate: unsupported POSTGRES_SSL_MODE: $SSL_MODE" >&2; exit 2 ;;
esac
command -v migrate >/dev/null 2>&1 || { echo "migrate: golang-migrate is required in PATH" >&2; exit 2; }

# Keep the password in the child environment, not in the DSN/argv or Make's
# echoed recipe. lib/pq reads PGPASSWORD when the URL has no password field.
export PGPASSWORD="$MIGRATE_PASSWORD"
DSN="postgres://$MIGRATE_USER@$HOST:$PORT/seev_$SERVICE?sslmode=$SSL_MODE&x-migrations-table=schema_migrations_$SERVICE"

if [[ "$COMMAND" == "up" ]]; then
	exec migrate -path "migrations/$SERVICE" -database "$DSN" up
fi
exec migrate -path "migrations/$SERVICE" -database "$DSN" down 1
