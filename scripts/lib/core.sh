#!/usr/bin/env bash
# Core helpers: logging, work-dir setup, dependency checks, environment fence.
# Sourced by scripts/lib.sh (compatibility aggregator). Never executed directly.
#
# Expected to be sourced AFTER the caller has set:
#   ROOT_DIR        — absolute repo root (callers: cd "$ROOT_DIR" first)
#   LIB_LOG_TAG     — tag shown in log output (e.g. "smoke", "chaos")
#   LIB_WORK_DIR_PREFIX — prefix for the mktemp directory name

# ─── Logging ─────────────────────────────────────────────────────────────────

log()  { printf '\033[1;34m[%s]\033[0m %s\n' "${LIB_LOG_TAG:-lib}" "$*"; }
ok()   { printf '\033[1;32m[ pass]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[ FAIL]\033[0m %s\n' "$*"; FAILED=1; }

FAILED=0

# ─── Dependency checks ───────────────────────────────────────────────────────

# require_command exits 2 with a clear message when a required command is absent.
require_command() {
    local cmd="$1" hint="${2:-}"
    command -v "$cmd" >/dev/null 2>&1 && return 0
    echo "lib/core.sh: required command '$cmd' not found in PATH" >&2
    [ -n "$hint" ] && echo "  Hint: $hint" >&2
    exit 2
}

# ─── Work directory ──────────────────────────────────────────────────────────
# (Setup logic is in lib.sh because it runs at source time before functions
# are defined — these helpers are for callers that need to validate the dir.)

# ─── Environment fence (P1.4) ────────────────────────────────────────────────
#
# Call assert_test_harness_environment before starting any infrastructure.
# Refuses production/staging environments and validates WORK_DIR isolation.
assert_test_harness_environment() {
    local env="${APP_ENV:-development}"
    case "$env" in
    development | test | ci | drill | load | gameday) ;;
    *)
        echo "lib/core.sh: refusing to start test harness with APP_ENV='${env}'" >&2
        echo "  Allowed values: development, test, ci, drill, load, gameday" >&2
        exit 1
        ;;
    esac

    local staging_envs="staging stage pre-prod preprod"
    for stage in $staging_envs; do
        if [ "$env" = "$stage" ]; then
            echo "lib/core.sh: refusing to use fixture secrets in a staging-class environment ('${env}')" >&2
            exit 1
        fi
    done

    if [ -z "${WORK_DIR:-}" ]; then
        echo "lib/core.sh: WORK_DIR is not set — must be set before assert_test_harness_environment" >&2
        exit 1
    fi
    local _tmpdir_ok=0
    case "$WORK_DIR" in
    /tmp/* | /var/tmp/*) _tmpdir_ok=1 ;;
    esac
    # Only test RUNNER_TEMP / TMPDIR when they are non-empty, to avoid
    # "$TMPDIR"/* expanding to /* (matching everything) when unset.
    if [ "$_tmpdir_ok" = "0" ] && [ -n "${RUNNER_TEMP:-}" ]; then
        case "$WORK_DIR" in
        "${RUNNER_TEMP}/"*) _tmpdir_ok=1 ;;
        esac
    fi
    if [ "$_tmpdir_ok" = "0" ] && [ -n "${TMPDIR:-}" ]; then
        case "$WORK_DIR" in
        "${TMPDIR}/"*) _tmpdir_ok=1 ;;
        esac
    fi
    if [ "$_tmpdir_ok" = "0" ]; then
        if [ "${SEEV_ALLOW_NONTEMP_WORK_DIR:-0}" != "1" ]; then
            echo "lib/core.sh: WORK_DIR '$WORK_DIR' is not in a standard temp directory" >&2
            echo "  Set SEEV_ALLOW_NONTEMP_WORK_DIR=1 to override (e.g. for CI runner paths)" >&2
            exit 1
        fi
    fi

    log "environment fence: APP_ENV=${env}, WORK_DIR=${WORK_DIR} — OK"
}

# ─── Common CLI contract (P2.9) ──────────────────────────────────────────────
#
# Ops scripts should call: eval "$(seev_parse_args "$@")" at the top of their
# arg-parsing section. It sets DRY_RUN=1 when --dry-run is passed and removes
# the flag from "$@", letting the caller handle the remaining positional args.
# Unknown --flags are rejected with exit 2. --help is handled by the caller's
# own usage() function, which parse_args calls before exiting 0.
#
# Usage pattern in a script:
#   usage() { cat <<'EOF' ... EOF; }
#   DRY_RUN=0
#   for arg in "$@"; do
#     case "$arg" in
#       --help|-h) usage; exit 0 ;;
#       --dry-run)  DRY_RUN=1 ;;
#       --*) echo "unknown option: $arg" >&2; usage; exit 2 ;;
#     esac
#   done

# seev_dry_run_announce <action> — prints a DRY-RUN notice and returns.
# Call instead of the real action when DRY_RUN=1.
seev_dry_run_announce() {
    printf '\033[1;33m[DRY-RUN]\033[0m %s\n' "$*"
}

# ─── Cross-platform helpers (P3.1) ───────────────────────────────────────────

# sha256_file <path> — prints the SHA-256 hex digest; works on Linux and macOS.
sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        echo "lib/core.sh: neither sha256sum nor shasum is available" >&2
        return 1
    fi
}

# utc_timestamp_ms — prints current UTC timestamp in milliseconds.
utc_timestamp_ms() {
    if command -v date >/dev/null 2>&1; then
        # GNU date supports %3N (milliseconds); macOS date does not.
        local ts
        if date -u +%s%3N >/dev/null 2>&1 && [ "$(date -u +%s%3N | wc -c)" -gt 5 ]; then
            date -u +%s%3N
        else
            # macOS fallback: use python3 if available.
            if command -v python3 >/dev/null 2>&1; then
                python3 -c 'import time; print(int(time.time() * 1000))'
            else
                # Last resort: seconds only.
                date -u +%s
            fi
        fi
    fi
}
