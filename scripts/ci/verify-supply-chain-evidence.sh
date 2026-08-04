#!/usr/bin/env bash
# Validate the retained protected-release evidence bundle produced by
# .github/workflows/release-provenance.yml. This does not create or contact a
# registry; it verifies that an operator supplied a complete, internally
# consistent bundle from a real protected release run.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  printf 'usage: %s <unpacked-protected-release-evidence-dir>\n' "$0" >&2
  exit 2
fi

evidence_dir="$1"
if [[ ! -d "$evidence_dir" ]]; then
  printf 'evidence directory does not exist: %s\n' "$evidence_dir" >&2
  exit 2
fi

required_files=(
  run-manifest.txt
  published-image.txt
  published-trivy.json
  published-sbom.cdx.json
  cosign-verify.json
  published-index.json
  attestation-manifest.json
  attestation-verification.txt
)
for file in "${required_files[@]}"; do
  if [[ ! -s "$evidence_dir/$file" ]]; then
    printf '::error::protected-release evidence is missing: %s\n' "$file" >&2
    exit 1
  fi
done

if ! grep -Eq '^schema=seev\.release-publish\.v1$' "$evidence_dir/run-manifest.txt"; then
  printf '::error::unexpected protected-release evidence schema\n' >&2
  exit 1
fi
if ! grep -Eq '^digest=sha256:[0-9a-f]{64}$' "$evidence_dir/published-image.txt"; then
  printf '::error::published evidence does not contain an immutable digest\n' >&2
  exit 1
fi

digest="$(awk -F= '$1 == "digest" { print $2; exit }' "$evidence_dir/published-image.txt")"
image_ref="$(awk -F= '$1 == "image_ref" { print $2; exit }' "$evidence_dir/published-image.txt")"
if [[ "$image_ref" != *"@${digest}" ]]; then
  printf '::error::published image reference is not bound to its recorded digest\n' >&2
  exit 1
fi

for json_file in \
  published-trivy.json \
  published-sbom.cdx.json \
  cosign-verify.json \
  published-index.json \
  attestation-manifest.json; do
  if ! jq -e 'type == "object" or type == "array"' "$evidence_dir/$json_file" >/dev/null; then
    printf '::error::invalid JSON evidence: %s\n' "$json_file" >&2
    exit 1
  fi
done

vulnerability_count="$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity == "HIGH" or .Severity == "CRITICAL")] | length' "$evidence_dir/published-trivy.json")"
if [[ "$vulnerability_count" -ne 0 ]]; then
  printf '::error::published Trivy evidence contains %s HIGH/CRITICAL findings\n' "$vulnerability_count" >&2
  exit 1
fi

if ! jq -e '(.bomFormat == "CycloneDX") and (.components | type == "array")' "$evidence_dir/published-sbom.cdx.json" >/dev/null; then
  printf '::error::published SBOM is not a CycloneDX document with components\n' >&2
  exit 1
fi
if ! jq -e '((type == "array") and (length > 0)) or ((type == "object") and (length > 0))' "$evidence_dir/cosign-verify.json" >/dev/null; then
  printf '::error::Cosign verification evidence is empty\n' >&2
  exit 1
fi
if ! jq -e --arg digest "$digest" '
  if type == "array" then
    any(.[]; .critical.image["docker-manifest-digest"] == $digest)
  else
    .critical.image["docker-manifest-digest"] == $digest
  end
' "$evidence_dir/cosign-verify.json" >/dev/null; then
  printf '::error::Cosign verification evidence is not bound to the published digest\n' >&2
  exit 1
fi
if ! jq -e '[.manifests[]? | select(.annotations["vnd.docker.reference.type"] == "attestation-manifest")] | length >= 1' "$evidence_dir/published-index.json" >/dev/null; then
  printf '::error::published image index has no OCI attestation manifest\n' >&2
  exit 1
fi
if ! jq -e '([.layers[]? | select(.annotations["in-toto.io/predicate-type"] == "https://spdx.dev/Document")] | length >= 1) and ([.layers[]? | select((.annotations["in-toto.io/predicate-type"] // "") | startswith("https://slsa.dev/provenance/"))] | length >= 1)' "$evidence_dir/attestation-manifest.json" >/dev/null; then
  printf '::error::attestation manifest is missing an SBOM or SLSA provenance layer\n' >&2
  exit 1
fi
if ! grep -Eq '^sbom_layers=[1-9][0-9]*$' "$evidence_dir/attestation-verification.txt" || \
   ! grep -Eq '^provenance_layers=[1-9][0-9]*$' "$evidence_dir/attestation-verification.txt"; then
  printf '::error::attestation verification record does not prove both layer types\n' >&2
  exit 1
fi

printf 'verify-supply-chain-evidence: protected-release bundle is complete for %s\n' "$image_ref"
