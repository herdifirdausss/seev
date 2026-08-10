#!/usr/bin/env bash
# check-promotion-evidence.sh — Gate that validates all required evidence
# is attached before a commit is treated as staging promotion evidence.
#
# Required environment:
#   RUNTIME_ACCEPTANCE_MANIFEST  path to a seev.runtime-acceptance.v1 manifest
#
# Optional environment:
#   LOAD_REPORT          path to a load report JSON produced by tools/loadreport
#   DR_EVIDENCE          path to a DR drill evidence file (dr-<commit>-*.txt)
#   HPA_EVIDENCE         path to deploy/load/hpa-evidence.yaml
#   PROMOTED_COMMIT      git commit SHA being promoted (validated against manifest)
#
# Exit code 0 = all required evidence is present and verified.
# Exit code 1 = one or more required evidence items are missing or invalid.
# Exit code 2 = usage/environment error.
#
# Called as part of the staging promotion workflow:
#   RUNTIME_ACCEPTANCE_MANIFEST=artifacts/runtime-manifest.txt ./scripts/ci/check-promotion-evidence.sh
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

failures=0

fail() {
	printf 'promotion-evidence: FAIL: %s\n' "$*" >&2
	failures=$((failures + 1))
}

pass() {
	printf 'promotion-evidence: PASS: %s\n' "$*"
}

skip() {
	printf 'promotion-evidence: SKIP: %s\n' "$*"
}

# ── Required: runtime-acceptance manifest ────────────────────────────────────

RUNTIME_ACCEPTANCE_MANIFEST="${RUNTIME_ACCEPTANCE_MANIFEST:-}"
if [[ -z "$RUNTIME_ACCEPTANCE_MANIFEST" ]]; then
	printf 'promotion-evidence: FAIL: RUNTIME_ACCEPTANCE_MANIFEST is required\n' >&2
	printf 'promotion-evidence: run make runtime-acceptance and set RUNTIME_ACCEPTANCE_MANIFEST=<path>\n' >&2
	exit 2
fi
if [[ ! -s "$RUNTIME_ACCEPTANCE_MANIFEST" ]]; then
	fail "runtime-acceptance manifest is missing or empty: $RUNTIME_ACCEPTANCE_MANIFEST"
else
	if "$root_dir/scripts/ci/check-runtime-acceptance.sh" "$RUNTIME_ACCEPTANCE_MANIFEST" >/dev/null 2>&1; then
		# Verify the manifest's commit matches PROMOTED_COMMIT when given
		manifest_commit="$(awk -F= '$1 == "commit" { value=$2 } END { print value }' \
			"$RUNTIME_ACCEPTANCE_MANIFEST")"
		if [[ -n "${PROMOTED_COMMIT:-}" && "$manifest_commit" != "$PROMOTED_COMMIT" ]]; then
			fail "runtime-acceptance manifest commit ($manifest_commit) does not match promoted commit ($PROMOTED_COMMIT)"
		else
			pass "runtime-acceptance manifest: schema=seev.runtime-acceptance.v1 commit=${manifest_commit}"
		fi
	else
		fail "runtime-acceptance manifest failed validation — see check-runtime-acceptance.sh output"
		"$root_dir/scripts/ci/check-runtime-acceptance.sh" "$RUNTIME_ACCEPTANCE_MANIFEST" 2>&1 |
			tail -5 >&2 || true
	fi
fi

# ── Optional: load test report ───────────────────────────────────────────────

LOAD_REPORT="${LOAD_REPORT:-}"
if [[ -n "$LOAD_REPORT" ]]; then
	if [[ ! -s "$LOAD_REPORT" ]]; then
		fail "load report file is missing or empty: $LOAD_REPORT"
	elif ! python3 -c "import json, sys; d=json.load(open(sys.argv[1])); assert d.get('overall') == 'pass'" \
		"$LOAD_REPORT" 2>/dev/null; then
		fail "load report overall result is not 'pass': $LOAD_REPORT"
	else
		pass "load report: overall=pass ($LOAD_REPORT)"
	fi
else
	skip "LOAD_REPORT not set — load test evidence not validated"
fi

# ── Optional: DR drill evidence ──────────────────────────────────────────────

DR_EVIDENCE="${DR_EVIDENCE:-}"
if [[ -n "$DR_EVIDENCE" ]]; then
	if [[ ! -s "$DR_EVIDENCE" ]]; then
		fail "DR evidence file is missing or empty: $DR_EVIDENCE"
	elif ! grep -qF 'overall=passed' "$DR_EVIDENCE" 2>/dev/null; then
		fail "DR evidence does not contain 'overall=passed': $DR_EVIDENCE"
	else
		rpo_seconds="$(grep 'rpo_seconds=' "$DR_EVIDENCE" | head -1 | cut -d= -f2 | tr -d '[:space:]' || true)"
		pass "DR evidence: overall=passed rpo=${rpo_seconds:-?}s ($DR_EVIDENCE)"
	fi
else
	skip "DR_EVIDENCE not set — disaster-recovery drill evidence not validated"
fi

# ── Optional: HPA saturation evidence ───────────────────────────────────────

HPA_EVIDENCE="${HPA_EVIDENCE:-$root_dir/deploy/load/hpa-evidence.yaml}"
if [[ -s "$HPA_EVIDENCE" ]]; then
	schema_version="$(grep -m1 'schema_version:' "$HPA_EVIDENCE" | awk '{print $2}' | tr -d '"' || true)"
	if [[ "$schema_version" == "1" ]]; then
		pass "HPA saturation evidence: schema_version=1 ($HPA_EVIDENCE)"
	else
		fail "HPA evidence schema_version is unexpected: '${schema_version:-<empty>}'"
	fi
else
	skip "no HPA evidence file at $HPA_EVIDENCE — run 'make load-hpa-evidence' after a steady-state load run"
fi

# ── Summary ──────────────────────────────────────────────────────────────────

printf '\n'
if [[ "$failures" -gt 0 ]]; then
	printf 'promotion-evidence: FAIL: %d required evidence item(s) missing or invalid\n' "$failures" >&2
	printf 'promotion-evidence: attach all required evidence before declaring a staging promotion\n' >&2
	exit 1
fi
printf 'promotion-evidence: PASS: all required evidence verified — promotion gate satisfied\n'
