#!/usr/bin/env bash
# Shell-level tests for safety guards and pure-logic helpers (P2.10).
#
# These tests do NOT start any services or containers — they are pure
# unit tests that exercise the shell safety guards added in P0.x–P2.x.
#
# Exit 0 = all tests pass; Exit 1 = at least one test failed.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

pass_count=0
fail_count=0

pass() {
	printf '\033[1;32m[ pass]\033[0m %s\n' "$*"
	pass_count=$((pass_count + 1))
}
fail() {
	printf '\033[1;31m[ FAIL]\033[0m %s\n' "$*"
	fail_count=$((fail_count + 1))
}

assert_exit() {
	local expected_exit="$1"
	local description="$2"
	shift 2
	local actual_exit=0
	"$@" >/dev/null 2>&1 || actual_exit=$?
	if [ "$actual_exit" -eq "$expected_exit" ]; then
		pass "$description (exit $actual_exit)"
	else
		fail "$description — expected exit $expected_exit, got $actual_exit"
	fi
}

assert_output_contains() {
	local pattern="$1"
	local description="$2"
	shift 2
	local out
	out="$("$@" 2>&1 || true)"
	if echo "$out" | grep -qE -- "$pattern"; then
		pass "$description"
	else
		fail "$description — expected output to contain pattern: $pattern"
		fail "  actual output: $out"
	fi
}

# ─── migrate.sh: --help exits 0 ──────────────────────────────────────────────
assert_exit 0 "migrate.sh --help exits 0" \
	bash "$ROOT_DIR/scripts/ops/migrate.sh" --help

assert_output_contains "usage:" "migrate.sh --help shows usage" \
	bash "$ROOT_DIR/scripts/ops/migrate.sh" --help

# ─── migrate.sh: unknown option exits 2 ──────────────────────────────────────
assert_exit 2 "migrate.sh --unknown exits 2" \
	bash "$ROOT_DIR/scripts/ops/migrate.sh" --unknown

# ─── migrate.sh: missing command exits 2 ─────────────────────────────────────
assert_exit 2 "migrate.sh with no args exits 2" \
	bash "$ROOT_DIR/scripts/ops/migrate.sh"

# ─── migrate.sh: missing SERVICE exits 2 ─────────────────────────────────────
assert_exit 2 "migrate.sh up without SERVICE exits 2" \
	bash "$ROOT_DIR/scripts/ops/migrate.sh" up

# ─── migrate.sh: invalid SERVICE exits 2 ─────────────────────────────────────
assert_exit 2 "migrate.sh up with invalid SERVICE exits 2" \
	env SERVICE=notaservice bash "$ROOT_DIR/scripts/ops/migrate.sh" up

# ─── migrate.sh: down without ACK exits 2 ────────────────────────────────────
assert_exit 2 "migrate.sh down without SEEV_MIGRATE_DOWN_ACK exits 2" \
	env SERVICE=ledger APP_ENV=development bash "$ROOT_DIR/scripts/ops/migrate.sh" down

# ─── migrate.sh: down with wrong ACK exits 2 ─────────────────────────────────
assert_exit 2 "migrate.sh down with wrong ACK exits 2" \
	env SERVICE=ledger APP_ENV=development SEEV_MIGRATE_DOWN_ACK=development:auth \
	bash "$ROOT_DIR/scripts/ops/migrate.sh" down

# ─── migrate.sh: dry-run up exits 0 (no migrate binary needed) ───────────────
assert_exit 0 "migrate.sh --dry-run up exits 0" \
	env SERVICE=ledger APP_ENV=development \
	bash "$ROOT_DIR/scripts/ops/migrate.sh" --dry-run up

assert_output_contains "DRY-RUN" "migrate.sh --dry-run up prints DRY-RUN" \
	env SERVICE=ledger APP_ENV=development \
	bash "$ROOT_DIR/scripts/ops/migrate.sh" --dry-run up

# ─── migrate.sh: dry-run down with correct ACK exits 0 ───────────────────────
assert_exit 0 "migrate.sh --dry-run down with correct ACK exits 0" \
	env SERVICE=ledger APP_ENV=development \
	SEEV_MIGRATE_DOWN_ACK=development:ledger \
	bash "$ROOT_DIR/scripts/ops/migrate.sh" --dry-run down

# ─── backup-checksums-enable.sh: --help prints usage ─────────────────────────
assert_output_contains "SEEV_CHECKSUM" \
	"backup-checksums-enable.sh --help shows SEEV_CHECKSUM_ACK" \
	bash "$ROOT_DIR/scripts/ops/backup-checksums-enable.sh" --help

# ─── product-assurance.sh: missing token exits 2 ─────────────────────────────
assert_exit 2 "product-assurance.sh without ASSURANCE_TOKEN exits 2" \
	env -u ASSURANCE_TOKEN bash "$ROOT_DIR/scripts/ops/product-assurance.sh" summary 2>/dev/null || true

# ─── product-assurance.sh: acknowledge with invalid UUID exits 2 ─────────────
assert_exit 2 "product-assurance.sh acknowledge with non-UUID exits 2" \
	env ASSURANCE_TOKEN=test bash "$ROOT_DIR/scripts/ops/product-assurance.sh" acknowledge "not-a-uuid" "reason"

# ─── product-assurance.sh: pause with invalid flow exits 2 ───────────────────
assert_exit 2 "product-assurance.sh pause with invalid flow exits 2" \
	env ASSURANCE_TOKEN=test bash "$ROOT_DIR/scripts/ops/product-assurance.sh" pause "badflow" \
	"550e8400-e29b-41d4-a716-446655440000" "1" "reason"

# ─── product-assurance.sh: pause with invalid UUID exits 2 ───────────────────
assert_exit 2 "product-assurance.sh pause with invalid UUID exits 2" \
	env ASSURANCE_TOKEN=test bash "$ROOT_DIR/scripts/ops/product-assurance.sh" pause payin \
	"not-a-uuid" "1" "reason"

# ─── product-assurance.sh: pause with non-integer revision exits 2 ───────────
assert_exit 2 "product-assurance.sh pause with non-integer revision exits 2" \
	env ASSURANCE_TOKEN=test bash "$ROOT_DIR/scripts/ops/product-assurance.sh" pause payin \
	"550e8400-e29b-41d4-a716-446655440000" "notanint" "reason"

# ─── assert_test_harness_environment: rejects production APP_ENV ─────────────
assert_exit 1 "assert_test_harness_environment rejects APP_ENV=production" \
	bash -c "
        ROOT_DIR='$ROOT_DIR'
        source '$ROOT_DIR/scripts/lib/core.sh'
        APP_ENV=production WORK_DIR=/tmp assert_test_harness_environment
    "

# ─── assert_test_harness_environment: accepts development APP_ENV ─────────────
assert_exit 0 "assert_test_harness_environment accepts APP_ENV=development" \
	bash -c "
        ROOT_DIR='$ROOT_DIR'
        source '$ROOT_DIR/scripts/lib/core.sh'
        APP_ENV=development WORK_DIR=/tmp/test-guard-$$ assert_test_harness_environment
    "

# ─── assert_test_harness_environment: rejects non-temp WORK_DIR ──────────────
assert_exit 1 "assert_test_harness_environment rejects non-temp WORK_DIR" \
	bash -c "
        ROOT_DIR='$ROOT_DIR'
        source '$ROOT_DIR/scripts/lib/core.sh'
        APP_ENV=development WORK_DIR=/home/some/path assert_test_harness_environment
    "

# ─── json_field: uses jq when available ──────────────────────────────────────
if command -v jq >/dev/null 2>&1; then
	out="$(
		source "$ROOT_DIR/scripts/lib/http.sh" 2>/dev/null
		echo '{"id":"abc123","status":"active"}' | json_field id
	)"
	if [ "$out" = "abc123" ]; then
		pass "json_field extracts flat string field with jq"
	else
		fail "json_field flat field — expected 'abc123', got '$out'"
	fi

	out="$(
		source "$ROOT_DIR/scripts/lib/http.sh" 2>/dev/null
		echo '{"data":{"id":"nested123"}}' | json_field data.id
	)"
	if [ "$out" = "nested123" ]; then
		pass "json_field extracts nested field with jq"
	else
		fail "json_field nested field — expected 'nested123', got '$out'"
	fi
fi

# ─── Summary ─────────────────────────────────────────────────────────────────
echo ""
echo "Results: $pass_count passed, $fail_count failed"
if [ "$fail_count" -gt 0 ]; then
	exit 1
fi
