#!/usr/bin/env bash
set -uo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
report_dir="${QUALITY_REPORT_DIR:-$root_dir/bin/quality-report}"

case "$report_dir" in
	"$root_dir/bin"/*) ;;
	*)
		printf 'quality-report: refusing output outside %s/bin: %s\n' "$root_dir" "$report_dir" >&2
		exit 2
		;;
esac

mkdir -p "$report_dir"
cd "$root_dir" || exit 2

run_gate() {
	local name="$1"
	shift
	local status=0
	"$@" >"$report_dir/$name.log" 2>&1 || status=$?
	printf '%s' "$status" >"$report_dir/$name.status"
	if [[ "$status" -eq 0 ]]; then
		printf 'quality-report: %-22s PASS\n' "$name"
	else
		printf 'quality-report: %-22s FAIL (%d; see %s)\n' "$name" "$status" "$report_dir/$name.log" >&2
	fi
}

run_gate architecture-metrics go run ./tools/architecturemetrics
run_gate coverage-policy make --no-print-directory business-coverage
run_gate lint make --no-print-directory lint
run_gate vulnerability make --no-print-directory security-vuln

architecture_status="$(<"$report_dir/architecture-metrics.status")"
coverage_status="$(<"$report_dir/coverage-policy.status")"
lint_status="$(<"$report_dir/lint.status")"
vulnerability_status="$(<"$report_dir/vulnerability.status")"
generated_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

printf '{\n  "schema_version": 1,\n  "generated_at": "%s",\n  "gates": {\n    "architecture_metrics": {"exit_code": %s},\n    "coverage_policy": {"exit_code": %s},\n    "lint": {"exit_code": %s},\n    "vulnerability": {"exit_code": %s}\n  }\n}\n' \
	"$generated_at" "$architecture_status" "$coverage_status" "$lint_status" "$vulnerability_status" \
	>"$report_dir/summary.json"

if [[ "$architecture_status" -ne 0 || "$coverage_status" -ne 0 || "$lint_status" -ne 0 || "$vulnerability_status" -ne 0 ]]; then
	exit 1
fi
printf 'quality-report: PASS (%s)\n' "$report_dir/summary.json"
