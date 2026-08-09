#!/usr/bin/env bash
# Declarative IaC/workload-identity chaos drill.  Render a valid cloud overlay,
# remove the provider annotation from the exact ServiceAccount consumed by the
# Vendor workload, and prove the rendered-manifest verifier rejects the fault.
# No cloud account or cluster is touched.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HELM_IMAGE="${HELM_IMAGE:-alpine/helm:3.17.3@sha256:d899e6316789fec04ee95300a18e454b7942539cbb3d89bde3e0655d6ca2e895}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/seev-platform-chaos.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

helm_run() {
	if command -v helm >/dev/null 2>&1; then
		helm "$@"
	else
		docker run --rm -v "$ROOT_DIR:/work" -w /work "$HELM_IMAGE" "$@"
	fi
}

cd "$ROOT_DIR"
helm_run template seev deploy/helm/seev --namespace seev-app \
	-f deploy/helm/seev/values.yaml -f deploy/helm/seev/values-aws-dev.yaml >"$TMP_DIR/aws-good.yaml"

"$ROOT_DIR/scripts/ci/check-rendered-workload-identity.sh" "$TMP_DIR/aws-good.yaml" aws

cp "$TMP_DIR/aws-good.yaml" "$TMP_DIR/aws-missing-identity.yaml"
sed -i.bak '/eks\.amazonaws\.com\/role-arn:/d' "$TMP_DIR/aws-missing-identity.yaml"
rm -f "$TMP_DIR/aws-missing-identity.yaml.bak"
if "$ROOT_DIR/scripts/ci/check-rendered-workload-identity.sh" "$TMP_DIR/aws-missing-identity.yaml" aws; then
	echo 'platform-chaos: missing workload identity annotation was not rejected' >&2
	exit 1
fi

helm_run template seev deploy/helm/seev --namespace seev-app \
	-f deploy/helm/seev/values.yaml -f deploy/helm/seev/values-local.yaml >"$TMP_DIR/local.yaml"
"$ROOT_DIR/scripts/ci/check-rendered-workload-identity.sh" "$TMP_DIR/local.yaml" local

printf 'platform-chaos: provider-identity removal was detected at the rendered workload boundary\n'
