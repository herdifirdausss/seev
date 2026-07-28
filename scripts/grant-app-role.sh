#!/usr/bin/env bash
set -euo pipefail

MIGRATE_USER="${POSTGRES_MIGRATE_USER:-seev}"
MIGRATE_PASSWORD="${POSTGRES_MIGRATE_PASSWORD:-seev}"
HOST="${POSTGRES_HOST:-localhost}"
PORT="${POSTGRES_PORT:-5433}"
SSL_MODE="${POSTGRES_SSL_MODE:-disable}"
APP_USER="${POSTGRES_USER:-seev_app}"

[[ "$MIGRATE_USER" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || { echo "grant-app-role: unsafe POSTGRES_MIGRATE_USER" >&2; exit 2; }
[[ "$APP_USER" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || { echo "grant-app-role: unsafe POSTGRES_USER" >&2; exit 2; }
[[ "$HOST" =~ ^[A-Za-z0-9._:-]+$ ]] || { echo "grant-app-role: unsafe POSTGRES_HOST" >&2; exit 2; }
[[ "$PORT" =~ ^[0-9]+$ && "$PORT" -ge 1 && "$PORT" -le 65535 ]] || { echo "grant-app-role: POSTGRES_PORT must be between 1 and 65535" >&2; exit 2; }
case "$SSL_MODE" in
	disable|allow|prefer|require|verify-ca|verify-full) ;;
	*) echo "grant-app-role: unsupported POSTGRES_SSL_MODE: $SSL_MODE" >&2; exit 2 ;;
esac
command -v psql >/dev/null 2>&1 || { echo "grant-app-role: psql is required in PATH" >&2; exit 2; }

export PGPASSWORD="$MIGRATE_PASSWORD"
DSN="postgres://$MIGRATE_USER@$HOST:$PORT/postgres?sslmode=$SSL_MODE"
exec psql "$DSN" -v ON_ERROR_STOP=1 -v target_user="$APP_USER" \
	-c 'GRANT app_service TO :"target_user";'
