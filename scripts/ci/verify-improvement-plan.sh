#!/usr/bin/env bash
# Repository-local closure gate for the world-class engineering plan.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
required=(
	docs/engineering/improvement-plan-tracker.md
	docs/engineering/capability-inventory.md
	docs/engineering/risk-register.md
	docs/engineering/golden-route.md
	docs/engineering/golden-route-failure-matrix.md
	docs/engineering/production-readiness-scorecard.md
	docs/acceptance/p0-p1-risk-gate.md
	docs/engineering/p3-decision-register.md
	docs/acceptance/critical-failures.md
	docs/operations/production-readiness-checklist.md
	docs/operations/slo.md
	docs/operations/backup-restore-evidence.md
	docs/deployment/environment-contract.md
	docs/deployment/vendor-sandbox.md
	docs/security/supply-chain.md
	docs/acceptance/supply-chain.md
	docs/security/independent-review-scope.md
	docs/acceptance/independent-security-review.md
	.github/roadmap/metadata.yml
	scripts/ci/check-environment-contract.sh
	scripts/ci/check-vendor-sandbox-config.sh
	scripts/ci/check-capability-inventory.sh
	scripts/ci/check-iac.sh
	scripts/ci/check-action-pins.sh
	scripts/ci/verify-p0-p1-risk-gate.sh
	scripts/ci/verify-supply-chain-evidence.sh
	deploy/terraform/aws/platform/main.tf
	deploy/terraform/gcp/platform/main.tf
	deploy/helm/seev/values-staging.yaml
	services/ledger/migrations/000046_chargeback_dispute_amount_deadline.up.sql
	services/ledger/migrations/000047_chargeback_dispute_terminal_immutability.up.sql
)

missing=0
for path in "${required[@]}"; do
	if [[ ! -s "$root_dir/$path" ]]; then
		printf '::error::missing required improvement-plan artifact: %s\n' "$path" >&2
		missing=1
	fi
done

if rg -n 'TODO|TBD|UNKNOWN' "$root_dir/docs/engineering/improvement-plan-tracker.md"; then
	printf '::error::tracker contains unresolved placeholder text\n' >&2
	missing=1
fi

if (( missing != 0 )); then
	exit 1
fi
"$root_dir/scripts/ci/check-capability-inventory.sh"
printf 'verify-improvement-plan: repository implementation and evidence index are present\n'
