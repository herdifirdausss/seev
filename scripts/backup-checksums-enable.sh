#!/usr/bin/env bash
set -euo pipefail

restart_postgres=0
cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [[ "$restart_postgres" -eq 1 ]]; then
		docker compose up -d postgres >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

restart_postgres=1
docker compose stop postgres
docker compose run --rm --no-deps postgres \
	sh -c 'pg_checksums --enable --pgdata=/var/lib/postgresql/data && pg_checksums --check --pgdata=/var/lib/postgresql/data'
