#!/usr/bin/env bash
# Repository-side platform E2E: Terraform/IAM source contracts are validated,
# then the Helm values are rendered into the workload manifests that consume
# those contracts. This is intentionally cloud-account free; a real Terraform
# apply and provider-side identity exchange remain environment evidence gates.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
HELM_IMAGE="alpine/helm:3.17.3@sha256:d899e6316789fec04ee95300a18e454b7942539cbb3d89bde3e0655d6ca2e895"
TMP_ROOT="${TMPDIR:-/tmp}"
TMP_DIR="$(mktemp -d "$TMP_ROOT/seev-platform-e2e.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

helm_run() {
	if command -v helm >/dev/null 2>&1; then
		helm "$@"
		return
	fi
	docker run --rm -v "$ROOT_DIR:/work" -w /work "$HELM_IMAGE" "$@"
}

cd "$ROOT_DIR"
"$ROOT_DIR/scripts/ci/check-platform-integration.sh"

helm_run template seev deploy/helm/seev --namespace seev-app \
	-f deploy/helm/seev/values.yaml \
	-f deploy/helm/seev/values-local.yaml >"$TMP_DIR/local.yaml"

for deployment in gateway auth ledger payin payout fraud admin-bff assurance vendor; do
	grep -Fq "name: $deployment-service" "$TMP_DIR/local.yaml" || {
		printf 'platform-e2e: rendered deployment is missing: %s-service\n' "$deployment" >&2
		exit 1
	}
done

for account in gateway auth ledger payin payout fraud admin-bff assurance vendor; do
	grep -Fq "serviceAccountName: seev-$account" "$TMP_DIR/local.yaml" || {
		printf 'platform-e2e: rendered workload is missing ServiceAccount wiring: seev-%s\n' "$account" >&2
		exit 1
	}
done

grep -Fq 'name: seev-vendor' "$TMP_DIR/local.yaml" || {
	printf 'platform-e2e: rendered vendor ServiceAccount is missing\n' >&2
	exit 1
}
grep -Fq 'name: seev-public' "$TMP_DIR/local.yaml" || {
	printf 'platform-e2e: rendered public Gateway is missing\n' >&2
	exit 1
}

printf 'platform-e2e: Terraform/IAM contracts rendered into all application workloads and Gateway resources\n'
