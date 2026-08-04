#!/usr/bin/env bash
# Fast repository-local supply-chain gate. The workflow jobs perform the
# actual vulnerability scan and registry verification; this script prevents
# those jobs from being silently weakened by source changes.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
failed=0

error() {
  printf '::error::%s\n' "$1" >&2
  failed=1
}

require_text() {
  local file="$1"
  local text="$2"
  local message="$3"
  if ! grep -qF -- "$text" "$file"; then
    error "$message"
  fi
}

# Every image build in this repository has an immutable base-image reference.
# Tags stay beside the digest so Dependabot and reviewers can identify the
# intended version without making the build depend on a mutable tag.
dockerfiles=(
  "$root_dir/Dockerfile"
  "$root_dir/deploy/kubernetes/migrations.Dockerfile"
  "$root_dir/deploy/backup/Dockerfile"
  "$root_dir/deploy/backup/agent.Dockerfile"
  "$root_dir/analytics/connect/Dockerfile"
)
for dockerfile in "${dockerfiles[@]}"; do
  if [[ ! -s "$dockerfile" ]]; then
    error "required Dockerfile is missing: ${dockerfile#"$root_dir"/}"
    continue
  fi
  from_count=0
  while IFS= read -r from_line; do
    [[ -n "$from_line" ]] || continue
    from_count=$((from_count + 1))
    if ! [[ "$from_line" =~ @sha256:[0-9a-f]{64} ]]; then
      error "every FROM in ${dockerfile#"$root_dir"/} must use a full image digest: $from_line"
    fi
  done < <(grep -E '^FROM[[:space:]]+' "$dockerfile" || true)
  if ((from_count == 0)); then
    error "Dockerfile has no FROM instruction: ${dockerfile#"$root_dir"/}"
    continue
  fi
done

syntax_files=(
  "$root_dir/Dockerfile"
  "$root_dir/deploy/kubernetes/migrations.Dockerfile"
  "$root_dir/deploy/backup/Dockerfile"
  "$root_dir/deploy/backup/agent.Dockerfile"
)
for dockerfile in "${syntax_files[@]}"; do
  if ! grep -Eq '^# syntax=.*@sha256:[0-9a-f]{64}$' "$dockerfile"; then
    error "Dockerfile frontend must be pinned by digest: ${dockerfile#"$root_dir"/}"
  fi
done

require_text "$root_dir/Dockerfile" 'FROM gcr.io/distroless/static-debian12:nonroot@sha256:' \
  "core runtime must use the minimal distroless non-root image"
require_text "$root_dir/Dockerfile" 'USER nonroot:nonroot' \
  "core runtime must run as nonroot:nonroot"
require_text "$root_dir/deploy/kubernetes/migrations.Dockerfile" 'USER nonroot:nonroot' \
  "migration runtime must run as nonroot:nonroot"
require_text "$root_dir/deploy/backup/agent.Dockerfile" 'USER postgres' \
  "backup-agent runtime must run as postgres"
require_text "$root_dir/deploy/backup/Dockerfile" 'USER postgres' \
  "backup runtime must explicitly run as postgres"
require_text "$root_dir/analytics/connect/Dockerfile" 'USER kafka' \
  "analytics Connect runtime must run as kafka"

# Local Compose is allowed to use locally-built `seev/*:dev` images, but every
# third-party image used by the core stack is immutable as well.
compose_file="$root_dir/docker-compose.yml"
for image in busybox redis rabbitmq axllent/mailpit; do
  if rg -n -- "^[[:space:]]*image:[[:space:]]+${image}[^@[:space:]]*:[^@[:space:]]+([[:space:]]|$)" "$compose_file"; then
    error "third-party Compose image is not digest-pinned: $image"
  fi
done

helm_values="$root_dir/deploy/helm/seev/values.yaml"
for image in postgres redis rabbitmq ubuntu/squid; do
  if rg -n -- "^[[:space:]]*image:[[:space:]]+${image}[^@[:space:]]*:[^@[:space:]]+([[:space:]]|$)" "$helm_values"; then
    error "Helm infrastructure image is not digest-pinned: $image"
  fi
done

ci_workflow="$root_dir/.github/workflows/ci.yml"
release_workflow="$root_dir/.github/workflows/release-provenance.yml"
helm_apps="$root_dir/deploy/helm/seev/templates/apps.yaml"
helm_migrations="$root_dir/deploy/helm/seev/templates/migration-job.yaml"

require_text "$ci_workflow" 'go mod verify' \
  "CI must verify Go module checksums"
require_text "$ci_workflow" 'aquasecurity/setup-trivy@' \
  "CI must install the pinned Trivy scanner"
require_text "$ci_workflow" 'trivy image' \
  "CI must scan built container images"
require_text "$ci_workflow" '--exit-code 1' \
  "CI container findings must fail the gate"
require_text "$ci_workflow" 'format cyclonedx' \
  "CI must generate a CycloneDX SBOM"
require_text "$ci_workflow" 'retention-days: 30' \
  "CI must retain container scan evidence"

require_text "$release_workflow" '--attest=type=provenance' \
  "release workflow must request provenance attestation"
require_text "$release_workflow" '--attest=type=sbom' \
  "release workflow must request SBOM attestation"
require_text "$release_workflow" 'generator=docker/buildkit-syft-scanner:stable-1@sha256:79e7b013cbec16bbb436f312819a49a4a57752b2270c1a9332ae1a10fcc82a68' \
  "release SBOM generator must be digest-pinned"
require_text "$release_workflow" '--metadata-file' \
  "release workflow must retain Buildx image metadata"
require_text "$release_workflow" 'trivy image' \
  "release workflow must scan the release image"
require_text "$release_workflow" '--exit-code 1' \
  "release container findings must fail the gate"
require_text "$release_workflow" 'format cyclonedx' \
  "release workflow must retain a release SBOM"
require_text "$release_workflow" 'retention-days: 90' \
  "release workflow must retain release evidence"
require_text "$release_workflow" 'cosign sign' \
  "protected publish must sign the immutable image digest"
require_text "$release_workflow" 'cosign verify' \
  "protected publish must verify the image signature"
require_text "$release_workflow" 'certificate-oidc-issuer https://token.actions.githubusercontent.com' \
  "protected publish must verify the GitHub OIDC issuer"
require_text "$release_workflow" 'imagetools inspect --raw' \
  "protected publish must verify attestation descriptors"
require_text "$release_workflow" 'name: protected-release' \
  "protected publish must use the protected-release GitHub Environment"
require_text "$release_workflow" 'id-token: write' \
  "protected publish must request OIDC identity tokens"

# Production app and migration Pods retain the runtime hardening contract.
for marker in \
  'runAsNonRoot: true' \
  'readOnlyRootFilesystem: true' \
  'allowPrivilegeEscalation: false' \
  'drop: [ALL]' \
  'type: RuntimeDefault'; do
  require_text "$helm_apps" "$marker" "application Helm template is missing runtime hardening: $marker"
done
for marker in \
  'runAsNonRoot: true' \
  'readOnlyRootFilesystem: true' \
  'allowPrivilegeEscalation: false' \
  'drop: [ALL]' \
  'type: RuntimeDefault'; do
  require_text "$helm_migrations" "$marker" "migration Helm template is missing runtime hardening: $marker"
done
require_text "$root_dir/deploy/helm/seev/values-staging.yaml" \
  'digest: sha256:REPLACE_WITH_IMMUTABLE_APPLICATION_DIGEST' \
  "staging application overlay must require immutable image evidence"
require_text "$root_dir/deploy/helm/seev/values-staging.yaml" \
  'digest: sha256:REPLACE_WITH_IMMUTABLE_MIGRATION_DIGEST' \
  "staging migration overlay must require immutable image evidence"

if (( failed != 0 )); then
  exit 1
fi

"$root_dir/scripts/ci/check-action-pins.sh"
printf 'supply-chain-check: pinned sources, scan/SBOM gates, and hardened runtimes passed; protected-registry evidence is release-scoped\n'
