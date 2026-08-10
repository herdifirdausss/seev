#!/usr/bin/env bash
# check-rendered-manifests.sh — DEPLOY-006 Helm render + Kubernetes schema +
# Seev policy-as-code validation.
#
# Renders every supported Helm overlay to a temp artifact, validates rendered
# resources against Kubernetes schemas (kubeconform), and checks Seev-specific
# deployment invariants (conftest/OPA).
#
# Prerequisites: helm, kubeconform, conftest (all must be on PATH)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HELM_CHART="${REPO_ROOT}/deploy/helm/seev"
POLICY_DIR="${REPO_ROOT}/deploy/policy"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/seev-rendered-manifests.XXXXXX")"
GITOPS_DIR="${REPO_ROOT}/deploy/gitops"

check_tool() {
	local tool="$1"
	if ! command -v "${tool}" >/dev/null 2>&1; then
		echo "error: ${tool} is required but not found on PATH" >&2
		exit 2
	fi
}

check_tool helm
check_tool kubeconform
check_tool conftest

trap 'rm -rf "${TMP_DIR}"' EXIT

OVERLAYS=(
	"base:"
	"local:${HELM_CHART}/values-local.yaml"
	"aws-dev:${HELM_CHART}/values-aws-dev.yaml"
	"gcp-dev:${HELM_CHART}/values-gcp-dev.yaml"
	"staging:${HELM_CHART}/values-staging.yaml|${HELM_CHART}/testdata/values-staging-resolved.yaml"
)

failures=0

echo "=== Rendered Manifest Validation (DEPLOY-006) ==="
echo ""

for overlay_entry in "${OVERLAYS[@]}"; do
	name="${overlay_entry%%:*}"
	values_spec="${overlay_entry#*:}"

	echo "-- Rendering overlay: ${name} --"
	out_file="${TMP_DIR}/${name}.yaml"

	if [[ -z "${values_spec}" ]]; then
		helm template seev "${HELM_CHART}" >"${out_file}"
	else
		render_args=(-f "${HELM_CHART}/values.yaml")
		IFS='|' read -r -a values_files <<<"${values_spec}"
		for values_file in "${values_files[@]}"; do
			render_args+=(-f "${values_file}")
		done
		helm template seev "${HELM_CHART}" "${render_args[@]}" >"${out_file}"
	fi
	echo "  rendered to: ${out_file}"

	echo "  running kubeconform..."
	if ! kubeconform \
		-schema-location default \
		-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
		-kubernetes-version 1.29.0 \
		-summary \
		-output text \
		"${out_file}"; then
		echo "  FAIL: kubeconform validation failed for overlay ${name}" >&2
		failures=$((failures + 1))
	else
		echo "  PASS: kubeconform schema validation"
	fi

	echo "  running conftest..."
	if ! conftest test \
		--policy "${POLICY_DIR}" \
		--all-namespaces \
		--quiet \
		--output table \
		"${out_file}"; then
		echo "  FAIL: policy validation failed for overlay ${name}" >&2
		failures=$((failures + 1))
	else
		echo "  PASS: Seev policy validation"
	fi
	echo ""
done

# --- Validate Argo CD manifests ---
echo "-- Validating GitOps Applications --"
GITOPS_FILES=()
while IFS= read -r -d '' f; do
	GITOPS_FILES+=("$f")
done < <(find "${GITOPS_DIR}" -name '*.yaml' -print0)

if [[ ${#GITOPS_FILES[@]} -gt 0 ]]; then
	if ! conftest test \
		--policy "${POLICY_DIR}" \
		--all-namespaces \
		--quiet \
		--output table \
		"${GITOPS_FILES[@]}"; then
		echo "FAIL: GitOps Application policy violations" >&2
		failures=$((failures + 1))
	else
		echo "PASS: GitOps Application policy validation"
	fi
fi
echo ""

echo "==================================="
if [[ "${failures}" -gt 0 ]]; then
	echo "RESULT: FAILED (${failures} overlay(s) failed)" >&2
	exit 1
else
	echo "RESULT: PASSED — all overlays pass schema and policy validation"
fi
