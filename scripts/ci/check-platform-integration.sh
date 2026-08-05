#!/usr/bin/env bash
# Validate the repository-side deployment integration contract.  This gate
# does not apply cloud resources: it validates Terraform modules, renders the
# Helm overlays, and proves that provider workload identity reaches the
# ServiceAccount consumed by the Vendor deployment.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
terraform_image="${TERRAFORM_IMAGE:-hashicorp/terraform:1.8.5}"
helm_image="${HELM_IMAGE:-alpine/helm:3.17.3@sha256:d899e6316789fec04ee95300a18e454b7942539cbb3d89bde3e0655d6ca2e895}"

if ! command -v terraform >/dev/null 2>&1 && ! command -v docker >/dev/null 2>&1; then
	printf 'check-platform-integration: terraform or Docker is required\n' >&2
	exit 2
fi
if ! command -v helm >/dev/null 2>&1 && ! command -v docker >/dev/null 2>&1; then
	printf 'check-platform-integration: helm or Docker is required\n' >&2
	exit 2
fi

terraform_run() {
	if command -v terraform >/dev/null 2>&1; then
		terraform "$@"
		return
	fi
	docker run --rm -v "$root_dir:/work" -w /work "$terraform_image" "$@"
}

helm_run() {
	if command -v helm >/dev/null 2>&1; then
		helm "$@"
		return
	fi
	docker run --rm -v "$root_dir:/work" -w /work "$helm_image" "$@"
}

require_file() {
	local path="$1"
	if [[ ! -s "$root_dir/$path" ]]; then
		printf 'check-platform-integration: missing required file: %s\n' "$path" >&2
		exit 1
	fi
}

require_text() {
	local path="$1"
	local text="$2"
	if ! grep -Fq -- "$text" "$root_dir/$path"; then
		printf 'check-platform-integration: %s is missing contract text: %s\n' "$path" "$text" >&2
		exit 1
	fi
}

cd "$root_dir"

for path in \
	deploy/terraform/aws/dev/versions.tf \
	deploy/terraform/aws/platform/versions.tf \
	deploy/terraform/gcp/dev/versions.tf \
	deploy/terraform/gcp/platform/versions.tf \
	deploy/helm/seev/Chart.yaml \
	deploy/helm/seev/templates/apps.yaml \
	deploy/helm/seev/templates/gateway-api.yaml \
	deploy/kubernetes/scripts/e2e.sh \
	deploy/helm/seev/templates/serviceaccounts.yaml; do
	require_file "$path"
done

# Terraform validation is deliberately backend-free and uses the same module
# set as check-iac.sh.  The Docker fallback keeps this gate usable on a clean
# developer machine where only Docker is installed.
terraform_run fmt -check -recursive deploy/terraform
for module in \
	deploy/terraform/aws/dev \
	deploy/terraform/aws/platform \
	deploy/terraform/gcp/dev \
	deploy/terraform/gcp/platform; do
	terraform_run "-chdir=$module" init -backend=false -input=false -upgrade=false >/dev/null
	terraform_run "-chdir=$module" validate
done

# Source contracts catch a provider module that still exists but no longer
# grants the exact identity used by the chart.  The cloud apply remains a
# separately authorized environment action.
require_text deploy/terraform/aws/dev/main.tf 'sts:AssumeRoleWithWebIdentity'
require_text deploy/terraform/aws/dev/main.tf 'system:serviceaccount:seev-app:seev-vendor'
require_text deploy/terraform/gcp/dev/main.tf "workload_pool = \"\${var.project_id}.svc.id.goog\""
require_text deploy/terraform/gcp/dev/main.tf 'seev-app/seev-vendor'
require_text deploy/helm/seev/templates/serviceaccounts.yaml 'serviceAccountAnnotations'
require_text deploy/helm/seev/templates/apps.yaml 'serviceAccountName:'
require_text deploy/helm/seev/templates/apps.yaml 'name: wait-for-data'
require_text deploy/helm/seev/templates/gateway-api.yaml 'value: /api/v1/users'
require_text deploy/kubernetes/scripts/e2e.sh 'RECIPIENT_ID='

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/seev-platform-integration.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

render_overlay() {
	local name="$1"
	shift
	helm_run lint deploy/helm/seev "$@"
	helm_run template seev deploy/helm/seev --namespace seev-app "$@" >"$tmp_dir/$name.yaml"
}

render_overlay local -f deploy/helm/seev/values-local.yaml
render_overlay gcp -f deploy/helm/seev/values-gcp-dev.yaml
render_overlay aws -f deploy/helm/seev/values-aws-dev.yaml
render_overlay staging -f deploy/helm/seev/values-staging.yaml

if ! grep -Fq 'serviceAccountName: seev-vendor' "$tmp_dir/gcp.yaml" || \
	! grep -Fq 'iam.gke.io/gcp-service-account:' "$tmp_dir/gcp.yaml"; then
	printf 'check-platform-integration: GCP workload identity did not render onto seev-vendor\n' >&2
	exit 1
fi
if ! grep -Fq 'serviceAccountName: seev-vendor' "$tmp_dir/aws.yaml" || \
	! grep -Fq 'eks.amazonaws.com/role-arn:' "$tmp_dir/aws.yaml"; then
	printf 'check-platform-integration: AWS workload identity did not render onto seev-vendor\n' >&2
	exit 1
fi
if grep -Fq 'iam.gke.io/gcp-service-account:' "$tmp_dir/local.yaml" || \
	grep -Fq 'eks.amazonaws.com/role-arn:' "$tmp_dir/local.yaml"; then
	printf 'check-platform-integration: local overlay unexpectedly renders a cloud identity annotation\n' >&2
	exit 1
fi

printf 'check-platform-integration: Terraform, Helm overlays, and AWS/GCP workload identity contracts passed\n'
