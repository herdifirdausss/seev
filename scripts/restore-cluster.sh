#!/usr/bin/env bash
# docs/roadmap/active/50 T3 (K7, K8, K12): isolated, fail-closed cluster restore for
# a PITR or latest disaster-recovery drill. Restores into this script's
# own dedicated seev-a7-drill Compose project
# (deploy/backup/restore-compose.yml) — never the real dev stack's
# `postgres` service or `seev_postgres_data` volume.
#
# K7 fail-closed guards this script enforces, in order:
#   1. refuses an unset/default ("seev") project name;
#   2. refuses an already-populated target volume unless FORCE_REUSE_VOLUME=1
#      (which removes ONLY the drill's own volume, never the real one);
#   3. the repository is always mounted read-only, both for the one-shot
#      restore container and the started Postgres instance;
#   4. never starts any application service — restore ends at "cluster
#      promoted and inventoried", T4's drverify gate comes before any
#      traffic is ever pointed at this data;
#   5. cleanup (see cleanup_drill below) always names the isolated project
#      explicitly and prints the exact volumes before removing anything —
#      never an unscoped `docker compose down -v`.
set -euo pipefail

usage() {
	cat >&2 <<'EOF'
Usage: scripts/restore-cluster.sh <latest|time|lsn> [target]
       scripts/restore-cluster.sh cleanup

  latest          Restore to the most recent point PITR allows (all
                   available WAL replayed).
  time <TARGET>   Restore to TARGET, a PostgreSQL timestamptz string
                   (e.g. "2026-07-22 15:00:00+07").
  lsn <TARGET>    Restore to TARGET, a WAL LSN (e.g. "0/7000060").
  cleanup         Tear down the drill project explicitly (prints the
                   exact volumes before removing them).

Environment:
  DRILL_PROJECT_NAME    Compose project name (default: seev-a7-drill).
                          Must never be empty or "seev" — refused loudly.
  FORCE_REUSE_VOLUME     Set to 1 to remove and recreate an existing,
                          already-populated drill volume instead of
                          refusing. Never touches seev_postgres_data.
  STAGE_REPORT_PATH      Where to write the stage-timing JSON report
                          (default: /tmp/seev-a7-drill-stages.json).
EOF
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PROJECT="${DRILL_PROJECT_NAME:-seev-a7-drill}"
IMAGE="seev/postgres-backup:${SEEV_IMAGE_TAG:-dev}"
REPO_HOST_PATH="$(cd "${BACKUP_REPO_PATH:-deploy/backup/repo}" 2>/dev/null && pwd || echo "")"
VOLUME="${PROJECT}_drill_postgres_data"
STAGE_REPORT="${STAGE_REPORT_PATH:-/tmp/seev-a7-drill-stages.json}"
MIGRATE_USER="${POSTGRES_MIGRATE_USER:-seev}"
SERVICES="ledger auth payin payout fraud gateway adminbff assurance"

# K7 guard #1: an isolated project name is not optional.
if [ -z "$PROJECT" ] || [ "$PROJECT" = "seev" ]; then
	echo "restore-cluster.sh: refusing project name '$PROJECT' — this must be an isolated drill project, never the default dev stack (K7)." >&2
	exit 1
fi

STAGE_LOG=()
record_stage() {
	local name="$1" at
	at="$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)"
	STAGE_LOG+=("${name}=${at}")
	echo "restore-cluster.sh: stage '${name}' at ${at}"
}

# --project-directory is required here: without it, Compose resolves
# restore-compose.yml's relative volume paths against the COMPOSE FILE's
# own directory (deploy/backup/), not the repo root — found live as a
# doubled path ("deploy/backup/deploy/backup/pgbackrest.conf") the first
# time this ran. $ROOT_DIR keeps every relative path in that file meaning
# exactly what it means in the main docker-compose.yml (repo-root-relative).
drill_compose() {
	docker compose --project-name "$PROJECT" --project-directory "$ROOT_DIR" -f deploy/backup/restore-compose.yml "$@"
}

# K7 guard #5: cleanup always names the project explicitly and prints the
# exact volumes first — callable directly (`restore-cluster.sh cleanup`)
# or automatically on a failed run below.
cleanup_drill() {
	echo "restore-cluster.sh: cleaning up project '$PROJECT' — target volumes:"
	docker volume ls --filter "name=^${PROJECT}_" --format '  {{.Name}}'
	drill_compose down -v --remove-orphans 2>/dev/null || true
	docker volume rm "$VOLUME" >/dev/null 2>&1 || true
}

if [ "${1:-}" = "cleanup" ]; then
	cleanup_drill
	exit 0
fi

MODE="${1:-}"
TARGET="${2:-}"
case "$MODE" in
latest) ;;
time | lsn)
	if [ -z "$TARGET" ]; then
		echo "restore-cluster.sh: mode '$MODE' requires a target value" >&2
		usage
		exit 2
	fi
	;;
*)
	usage
	exit 2
	;;
esac

if [ -z "$REPO_HOST_PATH" ]; then
	echo "restore-cluster.sh: backup repository directory not found (BACKUP_REPO_PATH=${BACKUP_REPO_PATH:-deploy/backup/repo}) — nothing to restore from." >&2
	exit 1
fi
if [ ! -d "$REPO_HOST_PATH/backup/seev" ]; then
	echo "restore-cluster.sh: no 'seev' stanza found under $REPO_HOST_PATH/backup — nothing to restore from." >&2
	exit 1
fi
REPO_PASSPHRASE_FILE="deploy/backup/secrets/pgbackrest_repo_passphrase"
if [ ! -f "$REPO_PASSPHRASE_FILE" ]; then
	echo "restore-cluster.sh: $REPO_PASSPHRASE_FILE not found — run 'make backup-secret' first." >&2
	exit 1
fi
REPO_PASSPHRASE="$(cat "$REPO_PASSPHRASE_FILE")"

record_stage "drill_start"

# K7 guard #2: refuse an already-populated target unless explicitly told
# to reuse it — checked via a throwaway container so this works
# identically regardless of where Docker actually stores volume data
# (irrelevant on Docker Desktop's VM-backed volumes).
if docker volume inspect "$VOLUME" >/dev/null 2>&1; then
	if docker run --rm -v "${VOLUME}:/data:ro" alpine test -f /data/PG_VERSION >/dev/null 2>&1; then
		if [ "${FORCE_REUSE_VOLUME:-0}" != "1" ]; then
			echo "restore-cluster.sh: volume '$VOLUME' already contains a PostgreSQL data directory — refusing (K7: restore requires an empty target). Set FORCE_REUSE_VOLUME=1 to remove and recreate it, or run '$0 cleanup' first." >&2
			exit 1
		fi
		echo "restore-cluster.sh: FORCE_REUSE_VOLUME=1 — removing existing '$VOLUME'"
		cleanup_drill
	fi
fi

record_stage "preflight_ok"

pgbackrest_oneshot() {
	docker run --rm --user postgres \
		-e PGBACKREST_REPO1_CIPHER_PASS="$REPO_PASSPHRASE" \
		-v "${REPO_HOST_PATH}:/backup-repo:ro" \
		-v "$(pwd)/deploy/backup/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
		--entrypoint pgbackrest \
		"$IMAGE" \
		--stanza=seev --config=/etc/pgbackrest/pgbackrest.conf "$@"
}

echo "restore-cluster.sh: repository info before restore:"
pgbackrest_oneshot info

RESTORE_ARGS=(--delta)
case "$MODE" in
latest)
	# Deliberately no --type/--target-action here. pgBackRest's default
	# restore type (no recovery target at all) makes PostgreSQL replay
	# every available WAL segment and then auto-promote once the archive
	# is exhausted — the correct native "latest" behavior. --target-action
	# is only valid alongside an explicit --type in
	# {immediate,lsn,name,time,xid} (found live: pgBackRest [031] rejects
	# --target-action=promote combined with the implicit default type).
	;;
time) RESTORE_ARGS+=(--type=time --target="$TARGET" --target-action=promote) ;;
lsn) RESTORE_ARGS+=(--type=lsn --target="$TARGET" --target-action=promote) ;;
esac

record_stage "restore_start"

docker volume create "$VOLUME" >/dev/null
docker run --rm --user postgres \
	-e PGBACKREST_REPO1_CIPHER_PASS="$REPO_PASSPHRASE" \
	-v "${VOLUME}:/var/lib/postgresql/data" \
	-v "${REPO_HOST_PATH}:/backup-repo:ro" \
	-v "$(pwd)/deploy/backup/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
	--entrypoint pgbackrest \
	"$IMAGE" \
	--stanza=seev --config=/etc/pgbackrest/pgbackrest.conf "${RESTORE_ARGS[@]}" restore

record_stage "restore_files_copied"

drill_compose up -d

record_stage "postgres_started"

echo "restore-cluster.sh: waiting for recovery to reach its target and promote..."
IN_RECOVERY="unknown"
TRIES=150
while [ "$TRIES" -gt 0 ]; do
	IN_RECOVERY="$(drill_compose exec -T postgres-restore \
		psql -U "$MIGRATE_USER" -d postgres -tAc "SELECT pg_is_in_recovery();" 2>/dev/null || echo "unknown")"
	if [ "$IN_RECOVERY" = "f" ]; then
		break
	fi
	sleep 2
	TRIES=$((TRIES - 1))
done
if [ "$IN_RECOVERY" != "f" ]; then
	echo "restore-cluster.sh: cluster did not promote within budget — inspect with: docker compose --project-name $PROJECT --project-directory . -f deploy/backup/restore-compose.yml logs postgres-restore" >&2
	exit 1
fi

record_stage "promoted"

RECOVERED_LSN="$(drill_compose exec -T postgres-restore \
	psql -U "$MIGRATE_USER" -d postgres -tAc "SELECT pg_last_wal_replay_lsn();")"
RECOVERED_TIME="$(drill_compose exec -T postgres-restore \
	psql -U "$MIGRATE_USER" -d postgres -tAc "SELECT now();")"
echo "restore-cluster.sh: promoted at replay LSN ${RECOVERED_LSN}, wall time ${RECOVERED_TIME}"

# K8: inventory databases, roles, extensions, and every service's
# migration version BEFORE any migration command is allowed to run
# against this restored cluster.
echo "restore-cluster.sh: inventory — databases, roles, extensions, migration versions"
MIGRATIONS_JSON="{}"
for service in $SERVICES; do
	database="seev_${service}"
	exists="$(drill_compose exec -T postgres-restore \
		psql -U "$MIGRATE_USER" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='${database}';" 2>/dev/null | tr -d '[:space:]')"
	if [ "$exists" != "1" ]; then
		echo "restore-cluster.sh: WARNING database ${database} missing from restored cluster" >&2
		continue
	fi
	row="$(drill_compose exec -T postgres-restore \
		psql -U "$MIGRATE_USER" -d "$database" -tAc "SELECT version || '|' || dirty FROM schema_migrations_${service};" 2>/dev/null || echo "|")"
	version="${row%%|*}"
	dirty="${row##*|}"
	echo "restore-cluster.sh:   ${database}: migration version=${version:-?} dirty=${dirty:-?}"
	MIGRATIONS_JSON="$(echo "$MIGRATIONS_JSON" | python3 -c "
import json, sys
d = json.load(sys.stdin)
d['${service}'] = {'version': int('${version:-0}' or 0), 'dirty': '${dirty}'.strip() == 't'}
print(json.dumps(d))
")"
done

record_stage "inventory_done"

python3 - "$STAGE_REPORT" "$MODE" "$TARGET" "$PROJECT" "$RECOVERED_LSN" "$RECOVERED_TIME" "$MIGRATIONS_JSON" "${STAGE_LOG[@]}" <<'PYEOF'
import json, sys

path, mode, target, project, recovered_lsn, recovered_time, migrations_json = sys.argv[1:8]
stage_pairs = sys.argv[8:]
stages = {}
for pair in stage_pairs:
    name, at = pair.split("=", 1)
    stages[name] = at

report = {
    "mode": mode,
    "target": target or None,
    "project": project,
    "recovered_lsn": recovered_lsn,
    "recovered_time": recovered_time,
    "migrations": json.loads(migrations_json),
    "stages": stages,
}
with open(path, "w") as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write("\n")
print(f"restore-cluster.sh: wrote {path}")
PYEOF

echo "restore-cluster.sh: drill complete. Project '$PROJECT' left running for inspection (T4 drverify runs next, before any traffic)."
echo "restore-cluster.sh: tear down explicitly when done: scripts/restore-cluster.sh cleanup"
