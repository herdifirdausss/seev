#!/usr/bin/env bash
# Supply-chain E2E for the repository boundary. It runs the immutable-source
# gate and then feeds a complete, deterministic protected-release-shaped
# bundle through the verifier. The fixture proves the digest/SBOM/provenance/
# signature consistency checks without pretending to be a real registry run.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
TMP_ROOT="${TMPDIR:-/tmp}"
BUNDLE_DIR="$(mktemp -d "$TMP_ROOT/seev-supply-chain-e2e.XXXXXX")"
trap 'rm -rf "$BUNDLE_DIR"' EXIT

command -v jq >/dev/null 2>&1 || {
	printf 'supply-chain-e2e: jq is required\n' >&2
	exit 2
}

"$ROOT_DIR/scripts/ci/supply-chain-check.sh"

DIGEST="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
IMAGE_REF="registry.example.invalid/seev/gateway@$DIGEST"
printf 'schema=seev.release-publish.v1\n' >"$BUNDLE_DIR/run-manifest.txt"
printf 'digest=%s\nimage_ref=%s\n' "$DIGEST" "$IMAGE_REF" >"$BUNDLE_DIR/published-image.txt"
printf '{"Results":[]}\n' >"$BUNDLE_DIR/published-trivy.json"
printf '{"bomFormat":"CycloneDX","components":[]}\n' >"$BUNDLE_DIR/published-sbom.cdx.json"
printf '[{"critical":{"image":{"docker-manifest-digest":"%s"}}}]\n' "$DIGEST" >"$BUNDLE_DIR/cosign-verify.json"
printf '{"manifests":[{"annotations":{"vnd.docker.reference.type":"attestation-manifest"}}]}\n' >"$BUNDLE_DIR/published-index.json"
printf '{"layers":[{"annotations":{"in-toto.io/predicate-type":"https://spdx.dev/Document"}},{"annotations":{"in-toto.io/predicate-type":"https://slsa.dev/provenance/v1"}}]}\n' >"$BUNDLE_DIR/attestation-manifest.json"
printf 'sbom_layers=1\nprovenance_layers=1\n' >"$BUNDLE_DIR/attestation-verification.txt"

"$ROOT_DIR/scripts/ci/verify-supply-chain-evidence.sh" "$BUNDLE_DIR"
printf 'supply-chain-e2e: source controls and protected-release evidence verifier passed\n'
