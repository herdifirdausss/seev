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
      scenarios/w1-p2p.js | scenarios/w2-webhook.js | scenarios/w3-payout.js | scenarios/w4-mixed.js | scenarios/w5-hotspot.js | scenarios/w6-resolver.js | scenarios/disbursement-burst.js)
        # Phase 0 §24.1/§24.2: all five canonical scenarios self-seed inside
        # k6's setup() (tests/load/lib/seed.js) — W1 a funded sender +
        # recipient, W2 a pool of pending topup intents plus a 10%
        # redelivery stream, W3 a funded sender for real withdrawals, W4 a
        # funded sender + recipient for K8's fixed action mix, W5 a pool
        # split for the K11 hot-account experiment. No operator-supplied
        # credentials required; deploy/load/compose.load.yaml already
        # defaults the admin identity k6 logs in as. disbursement-burst.js
        # (not one of W1-W7 — docs/performance/reports/2026-07-31-baseline.md
        # §16.3's B1 follow-up) self-seeds the same way, creating its own
        # disbursement batches via the real admin API. w6-resolver.js (B3's
        # experiment, §22) self-seeds one unfunded user — the fee-quote
        # endpoint it exercises never moves money.
        ;;
      *)
        [[ -n "${SEEV_LOAD_TOKEN:-}" ]] || die "business load scenarios require SEEV_LOAD_TOKEN from a disposable seeded user"
        ;;
    esac
  fi
  umask 077
  mkdir -p "$SECRET_DIR"
  for secret in seev_backup_password pgbackrest_repo_passphrase cryptox_key_v1 cryptox_lookup_key ledger_idempotency_key_v1 export_kek_v1 closure_kek_v1 merchant_api_key_pepper; do
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
  GOCACHE="${GOCACHE:-/tmp/seev-go-cache}" go run ./tools/loadcheck -profile "$PROFILE" -manifest "$ARTIFACT_ROOT/manifest.json" -write-manifest -run-id "$RUN_ID" -workload "${SEEV_LOAD_WORKLOAD:-bootstrap}" -dataset-hash "${SEEV_LOAD_DATASET_HASH:-sha256:0000000000000000000000000000000000000000000000000000000000000000}" -ack "$ACK" >/dev/null
  PROFILE_LOGICAL_CPUS="$(GOCACHE="${GOCACHE:-/tmp/seev-go-cache}" go run ./tools/loadcheck -profile "$PROFILE" -print-logical-cpus)"
  [[ "$PROFILE_LOGICAL_CPUS" =~ ^[1-9][0-9]*$ ]] || die "profile $PROFILE_ID returned an invalid logical CPU budget: $PROFILE_LOGICAL_CPUS"
  # Compose interprets deploy.resources.limits.cpus as a host CPU count. Keep
  # the profile as the single source of truth while allowing an explicit cap
  # for elasticity experiments; the compose file's fallback is only for
  # callers that bypass this runner.
  export SEEV_LOAD_CPUS_POSTGRES="${SEEV_LOAD_CPUS_POSTGRES:-$PROFILE_LOGICAL_CPUS}"
  export SEEV_LOAD_CPUS_LEDGER="${SEEV_LOAD_CPUS_LEDGER:-$PROFILE_LOGICAL_CPUS}"
}
compose() { docker compose -p "$PROJECT" -f docker-compose.yml -f deploy/load/compose.load.yaml --profile load "$@"; }
LEDGER_DB="seev_load_ledger"
LOAD_DB_USER="${SEEV_LOAD_DB_USER:-seev}"
# load_observer (pg_monitor, read-only) is provisioned by
# scripts/load-postgres-init/01-load-control-role.sh; the fixed password is
# disposable-stack-only, same convention as this script's other fixture
# secrets. Reached via the profile's published host port (loadprobe runs on
# the host, not inside the Compose network).
OBSERVER_DSN="host=127.0.0.1 port=${SEEV_LOAD_POSTGRES_PORT:-15433} user=load_observer password=B0observer000000000000000000000000000000000000000000000000000000 dbname=${LEDGER_DB} sslmode=disable"
DRAIN_TIMEOUT_SECONDS="${SEEV_LOAD_DRAIN_TIMEOUT_SECONDS:-90}"
DRAIN_POLL_SECONDS=2
# -U is mandatory here: `compose exec` runs as the postgres container's OS
# root user, and libpq falls back to that OS username when -U is absent —
# there is no Postgres role "root", so every call would silently fail
# (empty stdout, error on stderr) without this flag.
psql_load() { compose exec -T postgres psql -U "$LOAD_DB_USER" -v ON_ERROR_STOP=1 -t -A -d "$LEDGER_DB" -c "$1"; }

# start_container_stats samples `docker stats` for every container in this
# disposable project every 2s until stop_background_collectors kills it,
# writing resource-timeseries.jsonl. Phase 0 §24.4: the harness previously
# collected zero CPU/memory evidence — K10's process-resource gates (no
# OOM/restart, memory below 90%, no sustained CPU above 85%) had nothing to
# check against. Scoped to `compose ps -q` (this project's containers
# only): passing no IDs to `docker stats` samples every container on the
# host, which would leak unrelated data into this run's evidence.
start_container_stats() {
  ( while true; do
      local ts cids
      ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      # `docker compose ps -q` was used here originally and never returned
      # the load-generator container — a §26 Definition-of-Done gap this
      # session found: `compose ps` does not reliably list a one-off
      # `compose run --rm` container the way it lists `compose up -d`
      # services, so every retained resource-timeseries.jsonl silently had
      # zero samples for load-generator, and "was the generator ever the
      # bottleneck" was unanswerable from existing evidence, not just
      # unreviewed. Filtering by the compose project LABEL directly
      # (attached to every container regardless of how it was started)
      # catches it.
      cids="$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT")"
      if [[ -n "$cids" ]]; then
        # shellcheck disable=SC2086 # intentional word-splitting: multiple container IDs as separate args
        docker stats --no-stream --format '{{json .}}' $cids 2>/dev/null | while IFS= read -r line; do
          jq -cn --arg t "$ts" --argjson s "$line" '{timestamp: $t, stats: $s}'
        done >> "$ARTIFACT_ROOT/resource-timeseries.jsonl"
      fi
      sleep 2
    done ) &
  CONTAINER_STATS_PID=$!
}

# start_postgres_probe runs the existing (previously never-invoked)
# tools/loadprobe against the disposable ledger DB for PostgreSQL activity,
# lock waits, and top statements — Phase 0 §24.4's "PostgreSQL activity,
# waits, locks, top SQL, and sizes" bullet. Runs for a generous fixed
# duration and is killed early by stop_background_collectors once k6
# actually finishes, same pattern as start_container_stats. The ceiling was
# 10m until a Phase 3 60-minute soak discovered it silently truncated
# coverage to the run's first 10 minutes (docs/performance/reports/
# 2026-07-31-baseline.md §13.2) — 2h comfortably covers any run this
# harness realistically issues (including a 60-minute soak plus drain),
# while still being killed early for short runs, same as before.
#
# SEEV_LOAD_PROBE_INTERVAL defaults to 1s, safe for a multi-minute steady
# window or a 60-minute soak (4 lightweight catalog/stat queries per sample
# is negligible overhead at that cadence). A §16.4/§25-item-7 finding: 1s
# was too coarse to catch brief, sub-second lock contention in the
# disbursement-burst scenario (each burst run only lasts ~2-8s total, so a
# handful of 1s samples is a thin trace) — tools/loadprobe's own README has
# always documented `-interval 100ms` as supported; the harness runner
# simply never exposed it. Set this explicitly (e.g. `100ms`) for short,
# high-intensity scenarios; leave it at the default for long steady-state
# and soak runs, where finer sampling only adds probe overhead without
# adding evidence.
start_postgres_probe() {
  local interval="${SEEV_LOAD_PROBE_INTERVAL:-1s}"
  # Pre-built binary, not `go run ... &`: `go run` spawns the compiled
  # binary as its OWN child rather than exec-replacing itself, so `$!` here
  # used to capture the go-run WRAPPER's pid, not the actual worker's —
  # `stop_background_collectors`' `kill "$POSTGRES_PROBE_PID"` killed the
  # wrapper, which does not reliably forward the signal to (or wait for)
  # its child, leaving the real loadprobe process orphaned and still
  # polling indefinitely. Discovered live as the recurring cause of
  # loadprobe processes found running hours after their owning run had
  # supposedly finished — including holding a `load_observer` connection
  # open against a database `compose down -v` was actively trying to tear
  # down, which is a second, independent way this could have produced the
  # hangs this session hit. Building once up front and backgrounding the
  # real binary means `$!` is the actual worker's pid.
  local probe_bin="${GOCACHE:-/tmp/seev-go-cache}/bin-loadprobe"
  GOCACHE="${GOCACHE:-/tmp/seev-go-cache}" go build -o "$probe_bin" ./tools/loadprobe
  "$probe_bin" -dsn "$OBSERVER_DSN" -interval "$interval" -duration 2h -out "$ARTIFACT_ROOT/postgres-summary.jsonl" &
  POSTGRES_PROBE_PID=$!
}

# stop_background_collectors kills both samplers and reaps them so the
# script doesn't exit with orphaned background jobs. Safe to call whether
# or not either was started (SEEV_LOAD_RUN_ID lock scope, on_failure path).
stop_background_collectors() {
  if [[ -n "${CONTAINER_STATS_PID:-}" ]]; then
    kill "$CONTAINER_STATS_PID" 2>/dev/null || true
    wait "$CONTAINER_STATS_PID" 2>/dev/null || true
    CONTAINER_STATS_PID=""
  fi
  if [[ -n "${POSTGRES_PROBE_PID:-}" ]]; then
    kill "$POSTGRES_PROBE_PID" 2>/dev/null || true
    wait "$POSTGRES_PROBE_PID" 2>/dev/null || true
    POSTGRES_PROBE_PID=""
  fi
}

# collect_pool_summary queries the load Prometheus instance (already
# scraping every service's /metrics at 1s intervals per
# deploy/load/prometheus.yml — this data existed and was collected the
# whole time, just never queried) for the application connection-pool
# evidence Phase 0 §24.4 still needed: max in-use fraction and max wait
# count per service over a generous window covering the whole measurement
# phase. Subquery syntax (`[window:step]`) is required because the inner
# expression is a ratio, not a bare metric selector.
collect_pool_summary() {
  local prom_url="http://127.0.0.1:${SEEV_LOAD_PROMETHEUS_PORT:-19090}" window="30m"
  local in_use wait_count
  in_use="$(curl -s --data-urlencode "query=max by (job) (max_over_time((seev_database_pool_in_use_connections / seev_database_pool_max_open_connections)[${window}:1s]))" "${prom_url}/api/v1/query")"
  wait_count="$(curl -s --data-urlencode "query=max by (job) (max_over_time(seev_database_pool_wait_count_total[${window}]))" "${prom_url}/api/v1/query")"
  jq -n --argjson in_use "${in_use:-null}" --argjson wait_count "${wait_count:-null}" \
    '{max_pool_in_use_fraction_by_service: $in_use, max_pool_wait_count_by_service: $wait_count}' \
    > "$ARTIFACT_ROOT/pool-summary.json"
}

# collect_broker_summary reads RabbitMQ queue depth/consumer counts
# directly via rabbitmqctl inside the container — RabbitMQ's management
# port is deliberately not published to the host (kept internal-only, same
# as Redis), so this can't go through the Prometheus/host-port path
# collect_pool_summary uses. Phase 0 §24.4's "RabbitMQ... backlog" bullet.
collect_broker_summary() {
  local cid
  cid="$(compose ps -q rabbitmq 2>/dev/null)"
  if [[ -z "$cid" ]]; then
    echo '[]' > "$ARTIFACT_ROOT/broker-summary.json"
    return
  fi
  docker exec "$cid" rabbitmqctl list_queues name messages consumers --formatter json 2>/dev/null > "$ARTIFACT_ROOT/broker-summary.json" \
    || echo '[]' > "$ARTIFACT_ROOT/broker-summary.json"
}

# wait_for_outbox_drain polls the ledger outbox until zero rows are pending
# or DRAIN_TIMEOUT_SECONDS elapses, and writes outbox-summary.json. This is
# the check the old "drain" phase never actually did — it was a bare marker
# written after the disposable stack (and its Postgres volume) was already
# destroyed by `compose down -v`, so a stuck relay/consumer could never be
# detected (the 2026-07 baseline hit exactly this: 52 non-posted webhook
# rows with oldest age ~450s, discovered only after the fact from a leftover
# snapshot). Echoes elapsed seconds for the caller to record.
wait_for_outbox_drain() {
  local start elapsed=0 pending=-1 oldest_age=0 row
  start=$(date +%s)
  while (( elapsed < DRAIN_TIMEOUT_SECONDS )); do
    row="$(psql_load "SELECT count(*), COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at))), 0) FROM outbox_events WHERE status = 'pending';")"
    pending="$(cut -d'|' -f1 <<<"$row" | tr -d '[:space:]')"
    oldest_age="$(cut -d'|' -f2 <<<"$row" | tr -d '[:space:]')"
    [[ "$pending" == "0" ]] && break
    sleep "$DRAIN_POLL_SECONDS"
    elapsed=$(( $(date +%s) - start ))
  done
  elapsed=$(( $(date +%s) - start ))
  jq -n --arg pending "$pending" --arg oldest "$oldest_age" --arg elapsed "$elapsed" --arg timeout "$DRAIN_TIMEOUT_SECONDS" \
    '{pending_outbox_events: ($pending|tonumber), oldest_pending_age_seconds: ($oldest|tonumber), drain_seconds: ($elapsed|tonumber), timed_out: (($pending|tonumber) > 0), timeout_seconds: ($timeout|tonumber)}' \
    > "$ARTIFACT_ROOT/outbox-summary.json"
  echo "$elapsed"
}

# run_integrity_checks proves the run moved money correctly, using the same
# three assertions scripts/lib.sh runs against the dev stack
# (assert_ledger_balanced, assert_no_inconsistent_projections,
# assert_no_stuck_pending_transactions), scoped to the disposable load
# ledger DB and run while the stack is still alive. Writes
# <output_name>.json (default integrity-after.json — callers pass
# integrity-before.json for the pre-run snapshot Phase 1 qualification item
# 6 needs, "confirm pre/post integrity") and echoes "pass" or "fail".
run_integrity_checks() {
  local output_name="${1:-integrity-after.json}" unbalanced inconsistent stuck_pending passed=true
  unbalanced="$(psql_load "SELECT count(*) FROM fn_verify_ledger_balance('-infinity','infinity');" | tr -d '[:space:]')"
  inconsistent="$(psql_load "SELECT count(*) FROM v_account_balance_audit WHERE is_consistent = false;" | tr -d '[:space:]')"
  stuck_pending="$(psql_load "SELECT count(*) FROM ledger_transactions WHERE status = 'pending';" | tr -d '[:space:]')"
  [[ "$unbalanced" == "0" && "$inconsistent" == "0" && "$stuck_pending" == "0" ]] || passed=false
  jq -n --arg u "$unbalanced" --arg i "$inconsistent" --arg s "$stuck_pending" --argjson p "$passed" \
    '{unbalanced_transactions: ($u|tonumber), inconsistent_accounts: ($i|tonumber), stuck_pending_transactions: ($s|tonumber), passed: $p}' \
    > "$ARTIFACT_ROOT/$output_name"
  [[ "$passed" == "true" ]] && echo pass || echo fail
}

# hash_artifacts sha256-hashes every evidence file already written by the
# time patch_summary runs and returns a compact {"file":"sha256:..."} JSON
# object — Phase 1 qualification item 7 ("confirm raw artifact hashes"):
# summary.json's artifact_hashes field was always {} before this, so a
# report referencing "the raw artifact bundle" (§23.1) had nothing to
# verify it against. summary.json itself is excluded (it's the file being
# built, not evidence of something else) and run-status.json is written
# after this point, so it can't be included either.
hash_artifacts() {
  local file hash entries="[]"
  for file in manifest.json phases.jsonl resource-timeseries.jsonl postgres-summary.jsonl pool-summary.json broker-summary.json outbox-summary.json integrity-before.json integrity-after.json; do
    [[ -s "$ARTIFACT_ROOT/$file" ]] || continue
    hash="sha256:$(sha256sum "$ARTIFACT_ROOT/$file" | cut -d' ' -f1)"
    entries="$(jq -c --arg k "$file" --arg v "$hash" '. + [{key:$k, value:$v}]' <<<"$entries")"
  done
  jq -c 'from_entries' <<<"$entries"
}

# patch_summary rewrites the k6-produced summary.json with facts k6 cannot
# know at the time it writes its own output: drain time and ledger
# integrity, both only measurable after k6 exits and against the live
# stack. gate_passed is deliberately left false even on clean integrity —
# resource-timeseries.jsonl/postgres-summary.jsonl now capture raw CPU,
# memory, and PostgreSQL evidence (Phase 0 §24.4), but turning that into a
# pass/fail against deploy/load/thresholds.yaml is tools/loadreport's job
# (its own -thresholds flag), not this script's; claiming gate_passed=true
# here without running that evaluation would be a false green.
#
# smoke.js has no handleSummary() and so never writes summary.json — this
# is a no-op for bootstrap runs, not an error; outbox-summary.json and
# integrity-after.json are still written by the caller regardless.
patch_summary() {
  local drain_seconds=$1 integrity_result=$2 summary_file="$ARTIFACT_ROOT/summary.json" integrity_passed=false hashes
  [[ -s "$summary_file" ]] || return 0
  [[ "$integrity_result" == "pass" ]] && integrity_passed=true
  hashes="$(hash_artifacts)"
  jq --argjson ip "$integrity_passed" --argjson ds "$drain_seconds" --argjson h "$hashes" \
    '.integrity_passed = $ip | .drain_seconds = $ds | .artifact_hashes = $h' \
    "$summary_file" > "$summary_file.tmp"
  mv "$summary_file.tmp" "$summary_file"
}
STACK_STARTED=0
on_failure() {
  status=$?
  trap - EXIT INT TERM
  stop_background_collectors
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
    GOCACHE="${GOCACHE:-/tmp/seev-go-cache}" go run ./tools/loadcheck -profile "$PROFILE" -manifest "$ARTIFACT_ROOT/manifest.json"
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
    # Pre-run snapshot — Phase 1 qualification item 6 ("confirm pre/post
    # integrity"). A fresh disposable stack should always pass this (no
    # transactions exist yet); it exists as a baseline to diff against, and
    # to catch the disposable DB itself starting in a bad state.
    run_integrity_checks integrity-before.json >/dev/null
    phase measurement
    start_container_stats
    start_postgres_probe
    compose run --rm load-generator
    stop_background_collectors
    collect_pool_summary
    collect_broker_summary
    # Dataset manifest (§24.1 gap): k6's setup() seeds via real APIs as the
    # first step of the SAME `compose run` invocation above, so there is no
    # separate "seeded but not yet run" hook this script can observe —
    # this captures POST-RUN dataset state (accounts stay stable across
    # most scenarios; ledger_entries grow with the run itself), labeled as
    # such in the manifest's own fields, not a strict pre-load seed
    # snapshot. SEEV_LOAD_DATASET_TIER (D0/D1/D2), if set, checks the
    # resulting counts against docs/performance/reports/2026-xx-baseline.md
    # §4.2's declared tier bounds.
    # A plain string, not a bash array: this script's shebang is bash but
    # runs under macOS's stock /bin/bash 3.2 in practice, which treats an
    # EMPTY array as "unbound" under `set -u` (fixed with 4.4+) — discovered
    # live when this exact pattern crashed a real run. Matches $cids'
    # existing unquoted-word-splitting idiom above (start_container_stats).
    dataset_tier_arg=""
    if [[ -n "${SEEV_LOAD_DATASET_TIER:-}" ]]; then
      dataset_tier_arg="-tier $SEEV_LOAD_DATASET_TIER"
    fi
    # shellcheck disable=SC2086 # intentional word-splitting: dataset_tier_arg is either empty or exactly "-tier VALUE"
    GOCACHE="${GOCACHE:-/tmp/seev-go-cache}" go run ./tools/loaddataset -dsn "$OBSERVER_DSN" -run-id "$RUN_ID" $dataset_tier_arg -out "$ARTIFACT_ROOT/dataset-manifest.json" || true
    phase drain
    drain_seconds="$(wait_for_outbox_drain)"
    phase verification
    integrity_result="$(run_integrity_checks)"
    patch_summary "$drain_seconds" "$integrity_result"
    status=completed
    integrity_passed_json=true
    if [[ "$integrity_result" != "pass" ]]; then
      status=integrity_failed
      integrity_passed_json=false
    fi
    printf '{"status":"%s","scenario":"%s","project":"%s","drain_seconds":%s,"integrity_passed":%s}\n' \
      "$status" "$SCENARIO" "$PROJECT" "$drain_seconds" "$integrity_passed_json" > "$ARTIFACT_ROOT/run-status.json"
    if [[ "${SEEV_LOAD_KEEP_STACK:-0}" != "1" ]]; then
      compose down -v --remove-orphans
      STACK_STARTED=0
    fi
    [[ "$integrity_result" == "pass" ]] || die "ledger integrity check failed after run — see $ARTIFACT_ROOT/integrity-after.json"
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
