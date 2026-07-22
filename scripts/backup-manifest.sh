#!/usr/bin/env bash
# Writes a machine-readable manifest next to the most recent successful
# pgBackRest backup (docs/plan/50 K6) — invoked by `make backup-full`/
# `backup-diff` after pgbackrest itself reports success, never before.
# Contains no secrets or row data: backup identity/size/checksum status,
# PostgreSQL system identifier/timeline/LSN range, restorable time window,
# this repository's Git commit + dirty-tree indicator, every service's
# migration version/dirty flag, source label, and backup-tool version.
#
# Written atomically (temp file + rename) so a reader never observes a
# half-written manifest. Restore preflight (T3) rejects a missing file, an
# incomplete one, or a database list that doesn't match all eight
# authoritative databases exactly.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

MANIFEST_DIR="${BACKUP_MANIFEST_DIR:-deploy/backup/manifests}"
mkdir -p "$MANIFEST_DIR"

PROJECT_FLAG=()
if [ -n "${COMPOSE_PROJECT_NAME:-}" ]; then
    PROJECT_FLAG=(--project-name "$COMPOSE_PROJECT_NAME")
fi

compose_exec() {
    docker compose "${PROJECT_FLAG[@]}" exec -T \
        -e PGBACKREST_REPO1_CIPHER_PASS="$(cat deploy/backup/secrets/pgbackrest_repo_passphrase)" \
        postgres "$@"
}

INFO_JSON="$(compose_exec pgbackrest --stanza=seev --config=/etc/pgbackrest/pgbackrest.conf info --output=json)"

SERVICES="ledger auth payin payout fraud gateway adminbff assurance"
MIGRATIONS_JSON="{}"
for service in $SERVICES; do
    row="$(docker compose "${PROJECT_FLAG[@]}" exec -T postgres \
        psql -U "${POSTGRES_MIGRATE_USER:-seev}" -d "seev_${service}" -tAc \
        "SELECT version || '|' || dirty FROM schema_migrations_${service};")"
    version="${row%%|*}"
    dirty="${row##*|}"
    MIGRATIONS_JSON="$(echo "$MIGRATIONS_JSON" | python3 -c "
import json, sys
d = json.load(sys.stdin)
d['${service}'] = {'version': int('${version}'), 'dirty': '${dirty}'.strip() == 't'}
print(json.dumps(d))
")"
done

GIT_COMMIT="$(git rev-parse HEAD)"
GIT_DIRTY="false"
git diff --quiet --ignore-submodules HEAD -- || GIT_DIRTY="true"
git diff --quiet --ignore-submodules --cached HEAD -- || GIT_DIRTY="true"

python3 - "$INFO_JSON" "$MIGRATIONS_JSON" "$GIT_COMMIT" "$GIT_DIRTY" "$MANIFEST_DIR" "${SOURCE_ENV_LABEL:-local-dev}" <<'PYEOF'
import json, sys, datetime, os

info = json.loads(sys.argv[1])
migrations = json.loads(sys.argv[2])
git_commit = sys.argv[3]
git_dirty = sys.argv[4] == "true"
manifest_dir = sys.argv[5]
source_label = sys.argv[6]

stanza = info[0]
backups = stanza.get("backup", [])
if not backups:
    print("backup-manifest.sh: no backup found in pgbackrest info output — nothing to manifest", file=sys.stderr)
    sys.exit(1)
latest = backups[-1]
db = stanza["db"][0]
archive = stanza.get("archive", [{}])[0]

expected_databases = ["ledger", "auth", "payin", "payout", "fraud", "gateway", "adminbff", "assurance"]
missing = [s for s in expected_databases if s not in migrations]

manifest = {
    "backup_id": latest["label"],
    "backup_type": latest["type"],
    "status": "error" if latest.get("error") else "ok",
    "start_time": datetime.datetime.utcfromtimestamp(latest["timestamp"]["start"]).isoformat() + "Z",
    "end_time": datetime.datetime.utcfromtimestamp(latest["timestamp"]["stop"]).isoformat() + "Z",
    "size_bytes": latest["info"]["size"],
    "repository_size_bytes": latest["info"]["repository"]["size"],
    "checksum_status": "ok" if not latest.get("error") else "failed",
    "postgresql_version": db["version"],
    "system_identifier": db["system-id"],
    "start_lsn": latest["lsn"]["start"],
    "stop_lsn": latest["lsn"]["stop"],
    "oldest_archived_wal": archive.get("min"),
    "latest_archived_wal": archive.get("max"),
    "repository_git_commit": git_commit,
    "repository_dirty": git_dirty,
    "expected_databases": expected_databases,
    "migrations": migrations,
    "missing_migration_data": missing,
    "source_environment": source_label,
    "backup_tool_version": latest["backrest"]["version"],
    "encryption_enabled": stanza.get("cipher") is not None,
    "cipher_type": stanza.get("cipher"),
    "retention_policy": "2 full chains (repo1-retention-full=2)",
    "repository_check_result": stanza["status"]["message"],
}

out_path = os.path.join(manifest_dir, latest["label"] + ".json")
tmp_path = out_path + ".tmp"
with open(tmp_path, "w") as f:
    json.dump(manifest, f, indent=2, sort_keys=True)
    f.write("\n")
os.rename(tmp_path, out_path)
print(f"backup-manifest.sh: wrote {out_path}")
if missing:
    print(f"backup-manifest.sh: WARNING missing migration data for: {missing}", file=sys.stderr)
    sys.exit(1)
PYEOF
