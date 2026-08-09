#!/usr/bin/env bash
# Shared pgBackRest invocation helpers (P1.1).
#
# Both helpers read the repository passphrase INSIDE the container process —
# the value is never passed through host Docker CLI arguments, where it would
# appear in `ps` output and shell audit logs.
#
# Sourced by backup-manifest.sh, dr-drill.sh, restore-cluster.sh, and
# pgbackrest.sh; never executed directly.
#
# Required variables (callers set these before sourcing):
#   ROOT_DIR                   — repo root (absolute path)
#   PASSPHRASE_FILE            — host path to the passphrase file
#                                (default: deploy/backup/secrets/pgbackrest_repo_passphrase)

PGBACKREST_PASSPHRASE_FILE="${PASSPHRASE_FILE:-${ROOT_DIR:-$(pwd)}/deploy/backup/secrets/pgbackrest_repo_passphrase}"

# pgbackrest_preflight checks that the passphrase file exists and is non-empty.
# Call early so a missing secret fails with a clear message before any docker
# command is issued.
pgbackrest_preflight() {
	if [ ! -f "$PGBACKREST_PASSPHRASE_FILE" ]; then
		echo "backup.sh: $PGBACKREST_PASSPHRASE_FILE not found — run 'make backup-secret' first." >&2
		return 1
	fi
	if [ ! -s "$PGBACKREST_PASSPHRASE_FILE" ]; then
		echo "backup.sh: $PGBACKREST_PASSPHRASE_FILE is empty — re-run 'make backup-secret'." >&2
		return 1
	fi
}

# pgbackrest_compose_exec runs a pgBackRest command inside the already-running
# Compose postgres service. The passphrase is read from the secrets file inside
# the container shell — never interpolated into the host command line.
#
# Usage: pgbackrest_compose_exec [compose project flags...] <pgbackrest args...>
#
# Callers must set COMPOSE_PROJECT_FLAGS (array) before calling, e.g.:
#   COMPOSE_PROJECT_FLAGS=(--project-name myproject)
pgbackrest_compose_exec() {
	pgbackrest_preflight
	local passphrase_file_abs
	passphrase_file_abs="$(cd "$(dirname "$PGBACKREST_PASSPHRASE_FILE")" && pwd)/$(basename "$PGBACKREST_PASSPHRASE_FILE")"
	docker compose "${COMPOSE_PROJECT_FLAGS[@]:-}" exec -T postgres sh -c '
        set -eu
        export PGBACKREST_REPO1_CIPHER_PASS="$(cat "$1")"
        shift
        exec pgbackrest "$@"
    ' pgbackrest "$passphrase_file_abs" \
		--stanza=seev \
		--config=/etc/pgbackrest/pgbackrest.conf \
		"$@"
}

# pgbackrest_oneshot runs pgBackRest in a throwaway container (no running
# Compose stack required). The passphrase file is mounted read-only inside the
# container and read there — the host value never touches argv.
#
# Required args beyond pgBackRest args:
#   IMAGE          — container image with pgbackrest installed
#   REPO_HOST_PATH — absolute host path to the pgBackRest repository volume
#
# Callers set IMAGE and REPO_HOST_PATH before calling.
pgbackrest_oneshot() {
	pgbackrest_preflight
	local passphrase_file_abs
	passphrase_file_abs="$(cd "$(dirname "$PGBACKREST_PASSPHRASE_FILE")" && pwd)/$(basename "$PGBACKREST_PASSPHRASE_FILE")"
	docker run --rm --user postgres \
		-v "${passphrase_file_abs}:/run/secrets/pgbackrest_passphrase:ro" \
		-v "${REPO_HOST_PATH}:/backup-repo:ro" \
		-v "$(pwd)/deploy/backup/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
		--entrypoint sh \
		"$IMAGE" \
		-c 'set -eu; export PGBACKREST_REPO1_CIPHER_PASS="$(cat /run/secrets/pgbackrest_passphrase)"; exec pgbackrest "$@"' \
		pgbackrest \
		--stanza=seev \
		--config=/etc/pgbackrest/pgbackrest.conf \
		"$@"
}

# pgbackrest_oneshot_writable is identical to pgbackrest_oneshot except the
# DATA_VOLUME is mounted read-write (required for the restore operation, which
# writes the recovered cluster into the volume).
pgbackrest_oneshot_writable() {
	pgbackrest_preflight
	local passphrase_file_abs
	passphrase_file_abs="$(cd "$(dirname "$PGBACKREST_PASSPHRASE_FILE")" && pwd)/$(basename "$PGBACKREST_PASSPHRASE_FILE")"
	docker run --rm --user postgres \
		-v "${passphrase_file_abs}:/run/secrets/pgbackrest_passphrase:ro" \
		-v "${DATA_VOLUME}:/var/lib/postgresql/data" \
		-v "${REPO_HOST_PATH}:/backup-repo:ro" \
		-v "$(pwd)/deploy/backup/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
		--entrypoint sh \
		"$IMAGE" \
		-c 'set -eu; export PGBACKREST_REPO1_CIPHER_PASS="$(cat /run/secrets/pgbackrest_passphrase)"; exec pgbackrest "$@"' \
		pgbackrest \
		--stanza=seev \
		--config=/etc/pgbackrest/pgbackrest.conf \
		"$@"
}
