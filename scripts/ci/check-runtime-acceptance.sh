#!/usr/bin/env bash
# Validate a runtime-acceptance manifest produced by
# scripts/runtime-acceptance.sh. This checker is independent of the runner so
# a retained manifest can be reviewed again without rerunning a destructive
# drill.
set -euo pipefail

if [[ $# -ne 1 ]]; then
	printf 'usage: %s <runtime-manifest.txt>\n' "$0" >&2
	exit 2
fi

manifest="$1"
if [[ ! -s "$manifest" ]]; then
	printf 'check-runtime-acceptance: manifest is missing or empty: %s\n' "$manifest" >&2
	exit 2
fi

schema="$(awk -F= '$1 == "schema" { value=$2 } END { print value }' "$manifest")"
overall="$(awk -F= '$1 == "overall" { value=$2 } END { print value }' "$manifest")"
commit="$(awk -F= '$1 == "commit" { value=$2 } END { print value }' "$manifest")"
if [[ "$schema" != "seev.runtime-acceptance.v1" ]]; then
	printf 'check-runtime-acceptance: unexpected schema: %s\n' "${schema:-<empty>}" >&2
	exit 1
fi
if [[ ! "$commit" =~ ^[0-9a-f]{7,64}$ ]]; then
	printf 'check-runtime-acceptance: manifest has no valid commit: %s\n' "${commit:-<empty>}" >&2
	exit 1
fi
if [[ "$overall" != "passed" ]]; then
	printf 'check-runtime-acceptance: overall result is %s\n' "${overall:-<empty>}" >&2
	exit 1
fi

required_stages=(
	repository-unit
	app-business
	app-merchant
	app-capability
	app-privacy
	app-core-chaos
	app-capability-chaos
	container
	platform-integration
	platform-e2e
	platform-chaos
	supply-chain
	kubernetes-e2e
	kubernetes-rollout
	kubernetes-hpa-scale
	kubernetes-chaos
	disaster-recovery
)
for stage in "${required_stages[@]}"; do
	if ! awk -F '\t' -v wanted="$stage" '$1 == "stage" && $2 == wanted && $3 == "passed" { found=1 } END { exit(found ? 0 : 1) }' "$manifest"; then
		printf 'check-runtime-acceptance: required stage did not pass: %s\n' "$stage" >&2
		exit 1
	fi
done

required_capabilities=(
	"Ledger posting and idempotency"
	"Unified command policy boundary"
	"KYC/tenant execution state"
	"Scheduled and recurring transactions"
	"Fee pricing and approvals"
	"Pay-in and callback reconciliation"
	"Payout unknown-state recovery"
	"Disbursement maker-checker"
	"Chargeback dispute lifecycle"
	"Reconciliation and append-only corrections"
	"Multi-currency and FX"
	"Transactional outbox"
	"Auth and KYC"
	"Container/Kubernetes deployment"
	"IaC and workload identity"
	"Supply-chain controls"
	"DR and restore"
)

capability_count="$(awk -F '\t' '$1 == "capability" { count++ } END { print count + 0 }' "$manifest")"
if [[ "$capability_count" -ne "${#required_capabilities[@]}" ]]; then
	printf 'check-runtime-acceptance: expected %s capability rows, found %s\n' "${#required_capabilities[@]}" "$capability_count" >&2
	exit 1
fi

for capability in "${required_capabilities[@]}"; do
	if ! awk -F '\t' -v wanted="$capability" '$1 == "capability" && $2 == wanted && $3 == "accepted" && $4 != "" { found++ } END { exit(found == 1 ? 0 : 1) }' "$manifest"; then
		printf 'check-runtime-acceptance: capability is missing, duplicated, or not accepted: %s\n' "$capability" >&2
		exit 1
	fi
done

printf 'check-runtime-acceptance: %s capability rows and %s runtime stages passed\n' "${#required_capabilities[@]}" "${#required_stages[@]}"
