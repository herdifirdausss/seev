#!/usr/bin/env bash
# Write the repository's small, non-secret manifest for a critical-failure
# evidence bundle. The manifest is deliberately line-oriented so it can be
# inspected without jq and remains useful when a run fails before a test
# process starts.
set -euo pipefail

if [ "$#" -ne 1 ]; then
	printf 'usage: %s OUTPUT_DIR\n' "${0##*/}" >&2
	exit 2
fi

output_dir="$1"
mkdir -p "$output_dir"

value() {
	local name="$1"
	local fallback="$2"
	printf '%s' "${!name:-$fallback}"
}

{
	printf 'schema=seev-critical-failure-evidence.v1\n'
	printf 'evidence_kind=%s\n' "$(value SEEV_EVIDENCE_KIND unknown)"
	printf 'suite=%s\n' "$(value SEEV_EVIDENCE_SUITE unknown)"
	printf 'commit=%s\n' "$(value GITHUB_SHA unknown)"
	printf 'run_id=%s\n' "$(value GITHUB_RUN_ID local)"
	printf 'run_attempt=%s\n' "$(value GITHUB_RUN_ATTEMPT 1)"
	printf 'workflow=%s\n' "$(value GITHUB_WORKFLOW local)"
	printf 'event=%s\n' "$(value GITHUB_EVENT_NAME local)"
	printf 'ref=%s\n' "$(value GITHUB_REF local)"
	printf 'generated_at_utc=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
	printf 'workflow_step_outcome=%s\n' "$(value SEEV_EVIDENCE_OUTCOME unknown)"
	printf 'business_outcome=%s\n' "$(value SEEV_EVIDENCE_OUTCOME_BUSINESS unknown)"
	printf 'privacy_outcome=%s\n' "$(value SEEV_EVIDENCE_OUTCOME_PRIVACY unknown)"
	printf 'chaos_outcome=%s\n' "$(value SEEV_EVIDENCE_OUTCOME_CHAOS unknown)"
	printf 'integration_outcome=%s\n' "$(value SEEV_EVIDENCE_OUTCOME_INTEGRATION unknown)"
	printf 'retention_days=%s\n' "$(value SEEV_EVIDENCE_RETENTION_DAYS 30)"
	if [ -s "$output_dir/test-exit-code.txt" ]; then
		test_exit_code="$(sed -n 's/^test_exit_code=//p' "$output_dir/test-exit-code.txt" | head -n 1)"
		printf 'test_exit_code=%s\n' "${test_exit_code:-unknown}"
	else
		printf 'test_exit_code=unknown\n'
	fi
} >"$output_dir/run-manifest.txt"
