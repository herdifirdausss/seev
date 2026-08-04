#!/usr/bin/env bash
# Validate the repository-side contract for the P0/P1 release gate.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
risk_register="$root_dir/docs/engineering/risk-register.md"
scorecard="$root_dir/docs/engineering/production-readiness-scorecard.md"
packet="$root_dir/docs/acceptance/p0-p1-risk-gate.md"

required_files=(
	"$risk_register"
	"$scorecard"
	"$packet"
)

for path in "${required_files[@]}"; do
	if [[ ! -s "$path" ]]; then
		printf '::error::risk-gate: missing required file: %s\n' "$path" >&2
		exit 1
	fi
done

risk_ids=(
	R-001 R-002 R-003 R-004 R-005 R-006 R-007 R-008
	R-009 R-010 R-011 R-012 R-013 R-014 R-015
)

for risk_id in "${risk_ids[@]}"; do
	row_count="$(rg -c "^\\| ${risk_id} \\|" "$risk_register" || true)"
	if [[ "$row_count" != "1" ]]; then
		printf '::error::risk-gate: expected exactly one risk-register row for %s\n' "$risk_id" >&2
		exit 1
	fi

	row="$(rg "^\\| ${risk_id} \\|" "$risk_register")"
	if [[ ! "$row" =~ \|[[:space:]]P[01][[:space:]]\| ]]; then
		printf '::error::risk-gate: %s is not marked P0 or P1\n' "$risk_id" >&2
		exit 1
	fi

	IFS='|' read -r _ _ _ severity owner mitigation trigger response _ <<<"$row"
	for field_name in severity owner mitigation trigger response; do
		field_value="${!field_name}"
		field_value="${field_value//[[:space:]]/}"
		if [[ -z "$field_value" ]]; then
			printf '::error::risk-gate: %s has an empty %s field\n' "$risk_id" "$field_name" >&2
			exit 1
		fi
	done
done

scorecard_patterns=(
	'Security | no open P0/P1 security risk'
	'## Release blockers'
	'any open P0 correctness/security risk'
	'external evidence row still marked `evidence_required`'
	'release owner records the final decision, timestamp, commit, environment,'
	'approvers, and links to the evidence bundle.'
)
for pattern in "${scorecard_patterns[@]}"; do
	if ! rg -Fq "$pattern" "$scorecard"; then
		printf '::error::risk-gate: scorecard is missing required gate text: %s\n' "$pattern" >&2
		exit 1
	fi
done

packet_patterns=(
	'## Blocking risk inventory'
	'## Release approval record'
	'Final decision (`GO / NO-GO`)'
	'current evidence'
	'Until the release approval record is completed'
)
for pattern in "${packet_patterns[@]}"; do
	if ! rg -Fq "$pattern" "$packet"; then
		printf '::error::risk-gate: acceptance packet is missing required gate text: %s\n' "$pattern" >&2
		exit 1
	fi
done

printf 'risk-gate-check: P0/P1 inventory, scorecard, and release-approval contract passed; live evidence remains release-scoped\n'
