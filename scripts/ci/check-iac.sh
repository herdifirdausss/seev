#!/usr/bin/env bash
# Validate every Terraform module without contacting a cloud account.
# Provider installation is intentionally performed by Terraform itself; the
# backend is disabled so this check cannot read or mutate remote state.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if ! command -v terraform >/dev/null 2>&1; then
	printf 'check-iac: terraform is required (CI installs the pinned version)\n' >&2
	exit 2
fi

terraform fmt -check -recursive "$root_dir/deploy/terraform"

for module in \
	"$root_dir/deploy/terraform/aws/dev" \
	"$root_dir/deploy/terraform/aws/platform" \
	"$root_dir/deploy/terraform/gcp/dev" \
	"$root_dir/deploy/terraform/gcp/platform"; do
	if [[ ! -s "$module/versions.tf" ]]; then
		printf 'check-iac: missing Terraform module: %s\n' "${module#"$root_dir"/}" >&2
		exit 1
	fi
	terraform -chdir="$module" init -backend=false -input=false -upgrade=false >/dev/null
	terraform -chdir="$module" validate
done

printf 'check-iac: Terraform modules are formatted and validate without a backend\n'
