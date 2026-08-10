#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
minimum="${BUSINESS_COVERAGE_MIN:-30.0}"
profile="${COVERAGE_PROFILE:-}"
temp_dir=""

cleanup() {
	if [[ -n "$temp_dir" ]]; then
		rm -rf "$temp_dir"
	fi
}
trap cleanup EXIT

cd "$root_dir"
if [[ -z "$profile" ]]; then
	temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/seev-business-coverage.XXXXXX")"
	profile="$temp_dir/coverage.out"
	cover_packages="$(go list -f '{{.ImportPath}}' ./services/... ./internal/... ./operations/recovery/... | grep -vE '/cmd(/|$)' | paste -sd, -)"
	if [[ -z "$cover_packages" ]]; then
		printf 'business-coverage: no business packages found\n' >&2
		exit 2
	fi
	test_packages=()
	while IFS= read -r package; do
		if [[ -n "$package" ]]; then
			test_packages+=("$package")
		fi
	done < <(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./services/... ./internal/... ./operations/recovery/...)
	if [[ "${#test_packages[@]}" -eq 0 ]]; then
		printf 'business-coverage: no business test packages found\n' >&2
		exit 2
	fi
	if ! go test -covermode=atomic -coverpkg="$cover_packages" -coverprofile="$profile" "${test_packages[@]}" >"$temp_dir/go-test.log" 2>&1; then
		if ! grep -E '^(# |--- FAIL:|FAIL([[:space:]]|$)|panic:|fatal error:)' "$temp_dir/go-test.log" >&2; then
			tail -n 40 "$temp_dir/go-test.log" >&2
		fi
		exit 1
	fi
elif [[ "$profile" != /* ]]; then
	profile="$root_dir/$profile"
fi

go run ./tools/coveragecheck \
	-root "$root_dir" \
	-profile "$profile" \
	-minimum "$minimum" \
	-group "exact-money:${EXACT_MONEY_COVERAGE_MIN:-85}=internal/platform/money/" \
	-group "messaging:${MESSAGING_COVERAGE_MIN:-50}=internal/platform/messaging/" \
	-group "assurance-core:${ASSURANCE_COVERAGE_MIN:-25}=services/assurance/internal/assurance/,services/assurance/rules/" \
	-group "merchant-api:${MERCHANT_API_COVERAGE_MIN:-4}=services/gateway/internal/merchant/api/" \
	-group "balance-v2:${BALANCE_V2_COVERAGE_MIN:-6.5}=services/ledger/internal/migration/balancev2/" \
	-group "ledger-repository:${LEDGER_REPOSITORY_COVERAGE_MIN:-9}=services/ledger/internal/repository/" \
	-group "dr-verify-reseed:${DR_COVERAGE_MIN:-8}=operations/recovery/drverify/,operations/recovery/drreseed/"
