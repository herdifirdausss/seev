#!/usr/bin/env bash
set -euo pipefail

# Snapshot/restore is deliberately scoped to the disposable B0 Compose
# project. It never accepts a production-style database name or an arbitrary
# filesystem target.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
COMMAND="${1:-help}"
ACK="${SEEV_LOAD_ACK:-}"
RUN_ID="${SEEV_LOAD_RUN_ID:-}"
DB="${SEEV_LOAD_DATABASE:-seev_load_ledger}"
PROJECT="seev-load-${RUN_ID}"
ARTIFACT_ROOT="${SEEV_LOAD_ARTIFACT_ROOT:-artifacts/load/${RUN_ID}}"
SNAPSHOT_DIR="$ARTIFACT_ROOT/snapshots"

die() { echo "load-snapshot: $*" >&2; exit 1; }
[[ "$ACK" == "disposable-only" ]] || die "set SEEV_LOAD_ACK=disposable-only"
[[ "$RUN_ID" =~ ^[A-Za-z0-9T_.-]+$ && -n "$RUN_ID" ]] || die "unsafe or missing SEEV_LOAD_RUN_ID"
[[ "$DB" =~ ^seev_load_[a-z0-9_]+$ ]] || die "unsafe disposable database name"
[[ "$ARTIFACT_ROOT" == artifacts/load/* && "$ARTIFACT_ROOT" != artifacts/load/ ]] || die "unsafe artifact root"

compose() { docker compose -p "$PROJECT" -f docker-compose.yml -f deploy/load/compose.load.yaml --profile load "$@"; }
dump_path() { echo "$SNAPSHOT_DIR/${DB}.dump"; }

case "$COMMAND" in
  snapshot)
    mkdir -p "$SNAPSHOT_DIR"
    compose exec -T postgres pg_dump -Fc --no-owner --dbname="$DB" > "$(dump_path)"
    sha256sum "$(dump_path)" > "$(dump_path).sha256"
    chmod 0600 "$(dump_path)" "$(dump_path).sha256"
    echo "$(dump_path)"
    ;;
  restore)
    [[ -s "$(dump_path)" ]] || die "snapshot is missing: $(dump_path)"
    (cd "$ROOT_DIR" && sha256sum -c "$(dump_path).sha256")
    # The target DB is already created by the load-only init scripts. Restore
    # into that exact DB; pg_restore never receives a caller-controlled name.
    compose exec -T postgres pg_restore --clean --if-exists --no-owner --dbname="$DB" < "$(dump_path)"
    echo "restored $DB in $PROJECT"
    ;;
  *)
    echo "usage: $0 snapshot|restore"
    exit 2
    ;;
esac
