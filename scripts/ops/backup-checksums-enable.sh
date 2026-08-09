#!/usr/bin/env bash
# Enables page-level checksums on the PostgreSQL data directory (P1.3).
# This is a MUTATING, non-trivial operation: it stops the running postgres
# service, rewrites every data file page header, then restarts postgres.
#
# Safety class: S2 (persistent local mutation).
#
# Usage:
#   SEEV_CHECKSUM_ACK=<project> ./scripts/backup-checksums-enable.sh [--check]
#
#   --check    Inspect current checksum state without any mutation. No
#              acknowledgement required.
#
# Required environment:
#   SEEV_CHECKSUM_ACK    Must equal the resolved Compose project name.
#                        Prevents accidental mutation of the wrong project.
#
# Optional environment:
#   COMPOSE_PROJECT_NAME  Override the Compose project (default: seev).
set -euo pipefail

usage() {
	cat >&2 <<'EOF'
Usage: scripts/backup-checksums-enable.sh [--check]

  --check   Read-only: prints the current checksum state without mutation.
            No SEEV_CHECKSUM_ACK required.

Environment:
  SEEV_CHECKSUM_ACK=<project>   Required for mutation (not --check).
  COMPOSE_PROJECT_NAME          Compose project name (default: seev).

Example:
  COMPOSE_PROJECT_NAME=myproject SEEV_CHECKSUM_ACK=myproject \
    ./scripts/backup-checksums-enable.sh
EOF
}

CHECK_ONLY=0
for arg in "$@"; do
	case "$arg" in
	--check) CHECK_ONLY=1 ;;
	--help | -h)
		usage
		exit 0
		;;
	*)
		echo "backup-checksums-enable: unknown argument: $arg" >&2
		usage
		exit 2
		;;
	esac
done

PROJECT="${COMPOSE_PROJECT_NAME:-seev}"

if [ "$CHECK_ONLY" -eq 1 ]; then
	echo "backup-checksums-enable: checking checksum state for project '$PROJECT' (read-only)..."
	docker compose --project-name "$PROJECT" run --rm --no-deps postgres \
		sh -c 'pg_checksums --check --pgdata=/var/lib/postgresql/data 2>&1 || true'
	exit 0
fi

# ── Mutation path: acknowledgement required ───────────────────────────────────

PROTECTED_NAMES="seev production prod"
for name in $PROTECTED_NAMES; do
	if [ "$PROJECT" = "$name" ]; then
		echo "backup-checksums-enable: refusing project name '$PROJECT' — use a non-default project name or set COMPOSE_PROJECT_NAME." >&2
		exit 2
	fi
done

REQUIRED_ACK="$PROJECT"
PROVIDED_ACK="${SEEV_CHECKSUM_ACK:-}"

if [ -z "$PROVIDED_ACK" ]; then
	cat >&2 <<EOF
backup-checksums-enable: mutation requires explicit acknowledgement.

Set:
  SEEV_CHECKSUM_ACK=${REQUIRED_ACK}

Full example:
  COMPOSE_PROJECT_NAME=${PROJECT} SEEV_CHECKSUM_ACK=${PROJECT} \\
    ./scripts/backup-checksums-enable.sh

Use --check to inspect the current state without mutation.
EOF
	exit 2
fi

if [ "$PROVIDED_ACK" != "$REQUIRED_ACK" ]; then
	echo "backup-checksums-enable: acknowledgement mismatch — got '$PROVIDED_ACK', expected '$REQUIRED_ACK'" >&2
	exit 2
fi

echo "backup-checksums-enable: enabling checksums for project '$PROJECT'..."
echo "  This will stop postgres, rewrite data pages, then restart postgres."

restart_postgres=0
cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [[ "$restart_postgres" -eq 1 ]]; then
		echo "backup-checksums-enable: restarting postgres after interruption..."
		docker compose --project-name "$PROJECT" up -d postgres >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

restart_postgres=1
docker compose --project-name "$PROJECT" stop postgres
docker compose --project-name "$PROJECT" run --rm --no-deps postgres \
	sh -c 'pg_checksums --enable --pgdata=/var/lib/postgresql/data && pg_checksums --check --pgdata=/var/lib/postgresql/data'
restart_postgres=0
docker compose --project-name "$PROJECT" up -d postgres
echo "backup-checksums-enable: done — checksums enabled and verified for project '$PROJECT'."
