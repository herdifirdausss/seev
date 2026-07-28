#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

COMMAND="${1:-validate}"
PROFILE_ID="${SEEV_LOAD_PROFILE:-local-small}"
PROFILE="deploy/load/profiles/${PROFILE_ID}.yaml"
BASE_URL="${LOAD_BASE_URL:-http://127.0.0.1:8080}"
SCENARIO="${SEEV_LOAD_SCENARIO:-smoke.js}"
case "$SCENARIO" in
  W1) SCENARIO=scenarios/w1-p2p.js ;;
  W2) SCENARIO=scenarios/w2-webhook.js ;;
  W3) SCENARIO=scenarios/w3-payout.js ;;
  W4) SCENARIO=scenarios/w4-mixed.js ;;
  W5) SCENARIO=scenarios/w5-hotspot.js ;;
  W6) SCENARIO=scenarios/w6-resolver.js ;;
  W7) SCENARIO=scenarios/w7-size-ladder.js ;;
esac
export SEEV_LOAD_SCENARIO="$SCENARIO"
ACK="${SEEV_LOAD_ACK:-}"
RUN_ID="${SEEV_LOAD_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$(git rev-parse --short=12 HEAD)-${PROFILE_ID}}"
# Docker Compose project names are stricter than the manifest/run-id format:
# uppercase timestamp separators are valid metadata but invalid in a project
# name. Keep the original RUN_ID for evidence and use a normalized name only
# for the disposable Compose project.
PROJECT="seev-load-$(printf '%s' "$RUN_ID" | tr '[:upper:]' '[:lower:]')"
ARTIFACT_ROOT="${SEEV_LOAD_ARTIFACT_ROOT:-artifacts/load/${RUN_ID}}"
SECRET_DIR="${ROOT_DIR}/${ARTIFACT_ROOT}/secrets"
RUN_LOCK="${TMPDIR:-/tmp}/seev-load-b0.lock"
export SEEV_LOAD_RUN_ID="$RUN_ID"
export SEEV_LOAD_ARTIFACT_ROOT="$ARTIFACT_ROOT"
export SEEV_LOAD_SECRET_DIR="$SECRET_DIR"

die() { echo "load-test: $*" >&2; exit 1; }
phase() {
  local name=$1
  mkdir -p "$ARTIFACT_ROOT"
  printf '{"phase":"%s","at":"%s"}\n' "$name" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$ARTIFACT_ROOT/phases.jsonl"
}
acquire_run_lock() {
  if ! mkdir "$RUN_LOCK" 2>/dev/null; then
    die "another B0 load run is active (lock: $RUN_LOCK)"
  fi
  printf 'run_id=%s\nproject=%s\npid=%s\n' "$RUN_ID" "$PROJECT" "$$" > "$RUN_LOCK/owner"
  LOCK_HELD=1
}
release_run_lock() {
  if [[ "${LOCK_HELD:-0}" == "1" ]]; then
    rm -f -- "$RUN_LOCK/owner"
    rmdir -- "$RUN_LOCK" 2>/dev/null || true
    LOCK_HELD=0
  fi
}
preflight() {
  [[ "$ACK" == "disposable-only" ]] || die "set SEEV_LOAD_ACK=disposable-only"
  [[ -f "$PROFILE" ]] || die "unknown profile $PROFILE_ID"
  [[ "$RUN_ID" =~ ^[A-Za-z0-9T_.-]+$ ]] || die "unsafe run ID"
  [[ "$ARTIFACT_ROOT" == artifacts/load/* && "$ARTIFACT_ROOT" != artifacts/load/ ]] || die "unsafe artifact path"
  [[ "$SCENARIO" == "smoke.js" || "$SCENARIO" =~ ^scenarios/[a-z0-9-]+\.js$ ]] || die "unsafe load scenario"
  [[ -f "tests/load/$SCENARIO" ]] || die "load scenario not found: $SCENARIO"
  [[ "$BASE_URL" =~ ^https?://127\.0\.0\.1(:[0-9]+)?(/|$) ]] || die "base URL must be loopback"
  [[ "${APP_ENV:-load}" != "production" && "${SEEV_ENV:-load}" != "production" ]] || die "production mode is forbidden"
  [[ "${VENDOR_MOCKVENDOR_ENABLED:-true}" == "true" ]] || die "real vendor adapter is forbidden"
  [[ "${VENDOR_MOCKVENDOR_URL:-}" == "" && "${MOCKVENDOR_URL:-}" == "" ]] || die "real vendor URL is forbidden"
  if [[ "$SCENARIO" == scenarios/*.js ]]; then
    case "$SCENARIO" in
      scenarios/w2-webhook.js)
        [[ -n "${SEEV_LOAD_USER_ID:-}" && -n "${SEEV_LOAD_TOPUP_REFERENCE:-}" ]] || die "W2 requires SEEV_LOAD_USER_ID and SEEV_LOAD_TOPUP_REFERENCE from a disposable seeded intent"
        ;;
      scenarios/w1-p2p.js)
        [[ -n "${SEEV_LOAD_TARGET_USER_ID:-}" ]] || die "W1 requires SEEV_LOAD_TARGET_USER_ID from a disposable seeded recipient"
        ;;
      *)
        [[ -n "${SEEV_LOAD_TOKEN:-}" ]] || die "business load scenarios require SEEV_LOAD_TOKEN from a disposable seeded user"
        ;;
    esac
  fi
  umask 077
  mkdir -p "$SECRET_DIR"
  for secret in seev_backup_password pgbackrest_repo_passphrase cryptox_key_v1 cryptox_lookup_key ledger_idempotency_key_v1 export_kek_v1 closure_kek_v1; do
    if [[ ! -s "$SECRET_DIR/$secret" ]]; then
      case "$secret" in
        cryptox_key_v1) value=4444444444444444444444444444444444444444444444444444444444444444 ;;
        cryptox_lookup_key) value=5555555555555555555555555555555555555555555555555555555555555555 ;;
        ledger_idempotency_key_v1) value=1111111111111111111111111111111111111111111111111111111111111111 ;;
        export_kek_v1) value=2222222222222222222222222222222222222222222222222222222222222222 ;;
        closure_kek_v1) value=3333333333333333333333333333333333333333333333333333333333333333 ;;
        *) value=B0disposablebackup000000000000000000000000000000000000000000000000000000 ;;
      esac
      printf '%s\n' "$value" > "$SECRET_DIR/$secret"
    fi
  done
  GOCACHE="${GOCACHE:-/tmp/seev-go-cache}" go run ./cmd/loadcheck -profile "$PROFILE" -manifest "$ARTIFACT_ROOT/manifest.json" -write-manifest -run-id "$RUN_ID" -workload "${SEEV_LOAD_WORKLOAD:-bootstrap}" -dataset-hash "${SEEV_LOAD_DATASET_HASH:-sha256:0000000000000000000000000000000000000000000000000000000000000000}" -ack "$ACK" >/dev/null
}
compose() { docker compose -p "$PROJECT" -f docker-compose.yml -f deploy/load/compose.load.yaml --profile load "$@"; }
STACK_STARTED=0
on_failure() {
  status=$?
  trap - EXIT INT TERM
  if [[ "$STACK_STARTED" == 1 && "$status" -ne 0 ]]; then
    printf '{"status":"aborted","exit_code":%d}\n' "$status" > "$ARTIFACT_ROOT/run-status.json"
    compose down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  release_run_lock
  exit "$status"
}

case "$COMMAND" in
  validate)
    preflight
    GOCACHE="${GOCACHE:-/tmp/seev-go-cache}" go run ./cmd/loadcheck -profile "$PROFILE" -manifest "$ARTIFACT_ROOT/manifest.json"
    compose config >/dev/null
    echo "load-test: safety and compose preflight passed for $PROJECT"
    ;;
  manifest)
    preflight
    echo "$ARTIFACT_ROOT/manifest.json"
    ;;
  smoke|run)
    preflight
    acquire_run_lock
    trap on_failure EXIT INT TERM
    phase setup
    # Mark the project as owned before Compose starts creating resources. If
    # `up` fails halfway through (for example while binding a host port), the
    # EXIT trap must still remove the partial disposable stack and volumes.
    STACK_STARTED=1
    compose up --build -d
    phase measurement
    compose run --rm load-generator
    phase drain
    printf '{"status":"completed","scenario":"%s","project":"%s"}\n' "$SCENARIO" "$PROJECT" > "$ARTIFACT_ROOT/run-status.json"
    if [[ "${SEEV_LOAD_KEEP_STACK:-0}" != "1" ]]; then
      compose down -v --remove-orphans
      STACK_STARTED=0
    fi
    phase verification
    ;;
  clean)
    [[ -n "${SEEV_LOAD_RUN_ID:-}" ]] || die "set SEEV_LOAD_RUN_ID to the exact run you want to clean"
    [[ "$PROJECT" =~ ^seev-load-[A-Za-z0-9T_.-]+$ ]] || die "unsafe compose project"
    compose down -v --remove-orphans
    [[ "$ARTIFACT_ROOT" == artifacts/load/* && "$ARTIFACT_ROOT" != "artifacts/load/" ]] || die "unsafe artifact path"
    rm -rf -- "$ARTIFACT_ROOT"
    ;;
  *) die "usage: $0 validate|manifest|smoke|run|clean" ;;
esac
