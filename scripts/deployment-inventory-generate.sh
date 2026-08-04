#!/usr/bin/env bash
# Generate redacted, reproducible K0 evidence and the environment-key inventory.
# This script is read-only with respect to application/runtime state: it only
# writes the checked-in K0 evidence workspace and never starts containers or
# contacts a vendor.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVIDENCE_DIR="${ROOT_DIR}/docs/evidence/k0"
COMMAND_DIR="${EVIDENCE_DIR}/command-output"
GENERATED_DIR="${EVIDENCE_DIR}/generated"
CONFIGURATION_FILE="${ROOT_DIR}/deploy/inventory/configuration.yaml"

for command in git go docker jq ruby; do
  command -v "$command" >/dev/null || {
    echo "deployment-inventory-generate: $command is required" >&2
    exit 2
  }
done

mkdir -p "$COMMAND_DIR" "$GENERATED_DIR"

baseline_commit="$(git -C "$ROOT_DIR" rev-parse HEAD)"
baseline_branch="$(git -C "$ROOT_DIR" branch --show-current)"
captured_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
operator="$(id -un)"
machine_os="$(uname -srm)"
cpu_count="$(sysctl -n hw.ncpu 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || printf '%s' UNKNOWN)"
memory_bytes="$(sysctl -n hw.memsize 2>/dev/null || printf '%s' UNKNOWN)"
memory_gib="$(awk -v bytes="$memory_bytes" 'BEGIN { if (bytes ~ /^[0-9]+$/) printf "%.2f", bytes/1024/1024/1024; else print "UNKNOWN" }')"
free_space="$(df -h "$ROOT_DIR" | tail -n 1 | awk '{print $4 " available of " $2}')"

git -C "$ROOT_DIR" rev-parse HEAD >"$COMMAND_DIR/git-rev-parse-head.txt"
git -C "$ROOT_DIR" branch --show-current >"$COMMAND_DIR/git-branch.txt"
git -C "$ROOT_DIR" status --short >"$COMMAND_DIR/git-status-current.txt"
git -C "$ROOT_DIR" log -1 --format='%H%n%aI%n%s' >"$COMMAND_DIR/git-log-1.txt"
go version >"$COMMAND_DIR/go-version.txt"
docker version --format 'client={{.Client.Version}} server={{.Server.Version}}' >"$COMMAND_DIR/docker-version.txt"
docker compose version >"$COMMAND_DIR/docker-compose-version.txt"
uname -a >"$COMMAND_DIR/uname.txt"
df -h "$ROOT_DIR" >"$COMMAND_DIR/filesystem-free-space.txt"

docker info --format 'server={{.ServerVersion}}\narchitecture={{.Architecture}}\nos={{.OperatingSystem}}\nmem_total_bytes={{.MemTotal}}' \
  >"$COMMAND_DIR/docker-info-safe.txt" 2>&1 || true

find "$ROOT_DIR/services" "$ROOT_DIR/tools" "$ROOT_DIR/operations" -type f -name main.go -print | sort >"$COMMAND_DIR/entrypoint-files.txt"
go list ./services/... ./tools/... ./operations/... | sort >"$COMMAND_DIR/go-entrypoint-packages.txt"
docker compose config --services | sort >"$COMMAND_DIR/compose-base-services.txt"
docker compose --profile app config --services | sort >"$COMMAND_DIR/compose-app-services.txt"
docker compose --profile app config --images | sort -u >"$COMMAND_DIR/compose-images.txt"
docker compose --profile app config --format json --no-interpolate --no-env-resolution \
  | jq -S '.services |= with_entries(.value.environment = ((.value.environment // {}) | with_entries(.value = "<redacted>"))) | .secrets |= with_entries(.value.file = "<redacted>")' \
  >"$GENERATED_DIR/compose-normalized.json"
ruby -rjson -ryaml -e 'puts YAML.dump(JSON.parse(STDIN.read))' \
  <"$GENERATED_DIR/compose-normalized.json" \
  | sed -E 's/[[:blank:]]+$//' >"$EVIDENCE_DIR/compose-normalized.yaml"
rm -f "$GENERATED_DIR/compose-normalized.json"

docker compose --profile app config --format json --no-interpolate --no-env-resolution \
  | jq -S '{services: (.services | with_entries({key: .key, value: {image: .value.image, build_service: (.value.build.args.SERVICE // null), ports: (.value.ports // []), profiles: (.value.profiles // []), depends_on: (.value.depends_on // {})}})), volumes: (.volumes // {})}' \
  >"$GENERATED_DIR/compose-runtime-shape.json"

rg -n 'getenv|os\\.Getenv|os\\.LookupEnv|requireValue|parseDuration' \
  "$ROOT_DIR/internal/platform/config" "$ROOT_DIR/services" "$ROOT_DIR/tools" "$ROOT_DIR/operations" "$ROOT_DIR/internal" \
  --glob '*.go' >"$COMMAND_DIR/configuration-key-sources.txt" || true

image_list="$(docker image ls --format '{{.Repository}}:{{.Tag}}' | rg '^seev/' | sort -u || true)"
if [ -n "$image_list" ]; then
  while IFS= read -r image; do
    docker image inspect "$image" | jq '.[0] | {repo_tags: .RepoTags, repo_digests: .RepoDigests, id: .Id, created: .Created, size_bytes: .Size, architecture: .Architecture, os: .Os, user: .Config.User, entrypoint: .Config.Entrypoint, working_dir: .Config.WorkingDir, exposed_ports: (.Config.ExposedPorts // {})}'
  done <<<"$image_list" | jq -s '.' >"$GENERATED_DIR/image-inventory.json"
else
  printf '[]\n' >"$GENERATED_DIR/image-inventory.json"
fi

key_file="$(mktemp)"
trap 'rm -f "$key_file"' EXIT
sed -n 's/^[[:space:]]*#\{0,1\}[[:space:]]*\([A-Z][A-Z0-9_]*\)=.*/\1/p' "$ROOT_DIR/.env.example" >>"$key_file" || true
rg -o 'getenv\("[A-Z][A-Z0-9_]*"\)|os\.Getenv\("[A-Z][A-Z0-9_]*"\)|os\.LookupEnv\("[A-Z][A-Z0-9_]*"\)' \
  "$ROOT_DIR/internal" "$ROOT_DIR/services" "$ROOT_DIR/tools" "$ROOT_DIR/operations" --glob '*.go' \
  | sed -E 's/.*(getenv|Getenv|LookupEnv)\("([A-Z][A-Z0-9_]*)"\).*/\2/' >>"$key_file" || true
rg -o 'getWithDefault\(getenv, "[A-Z][A-Z0-9_]*"' \
  "$ROOT_DIR/internal" "$ROOT_DIR/services" "$ROOT_DIR/tools" "$ROOT_DIR/operations" --glob '*.go' \
  | sed -E 's/.*getWithDefault\(getenv, "([A-Z][A-Z0-9_]*)"/\1/' >>"$key_file" || true
sort -u "$key_file" >"$GENERATED_DIR/configuration-key-names.txt"

owner_for_key() {
  case "$1" in
    LEDGER_*|OUTBOX_*|FEE_*|SCHEDULE_*|INTEREST_*|WORKER_*) printf '%s' ledger ;;
    AUTH_*|KYC_*|CLOSURE_*|EXPORT_*|OBJECT_STORE_*) printf '%s' auth ;;
    PAYIN_*|TOPUP_*) printf '%s' payin ;;
    PAYOUT_*) printf '%s' payout ;;
    FRAUD_*|SCREENING_*) printf '%s' fraud ;;
    VENDOR_*|MOCKVENDOR_*) printf '%s' vendor ;;
    ADMIN_BFF_*) printf '%s' admin-bff ;;
    ASSURANCE_*|ALERT_*) printf '%s' assurance ;;
    GATEWAY_*|MERCHANT_*) printf '%s' gateway ;;
    *) printf '%s' shared-platform ;;
  esac
}

is_sensitive() {
  [[ "$1" =~ (PASSWORD|SECRET|TOKEN|KEY|PEPPER|PASSPHRASE|PRIVATE|CREDENTIAL|API_KEY) ]]
}

{
  printf '%s\n' 'version: 1'
  printf 'baseline_commit: %s\n' "$baseline_commit"
  printf '%s\n' 'generated: true'
  printf '%s\n' 'value_policy: configuration names only; values are never recorded'
  printf '%s\n' 'keys:'
  while IFS= read -r key; do
    [ -n "$key" ] || continue
    owner="$(owner_for_key "$key")"
    if is_sensitive "$key"; then
      target=Secret
      sensitive=true
    else
      target=ConfigMap
      sensitive=false
    fi
    printf '  - name: %s\n' "$key"
    printf '    owner: %s\n' "$owner"
    printf '    sensitive: %s\n' "$sensitive"
    printf '    kubernetes_target: %s\n' "$target"
    printf '    required: UNKNOWN\n'
    printf '    default: UNKNOWN\n'
    printf '    reloadable: UNKNOWN\n'
    printf '    source_refs: [.env.example, internal/platform/config, Compose/service wiring]\n'
  done <"$GENERATED_DIR/configuration-key-names.txt"
} >"$CONFIGURATION_FILE"

cat >"$EVIDENCE_DIR/baseline.md" <<EOF
# K0 baseline evidence

Captured: ${captured_at}

## Pinned baseline

- Commit: ${baseline_commit}
- Branch: ${baseline_branch}
- Operator: ${operator}
- Machine OS/architecture: ${machine_os}
- CPU count: ${cpu_count}
- Host memory: ${memory_gib} GiB (raw value is intentionally not repeated)
- Filesystem: ${free_space}
- Go: see [go-version.txt](command-output/go-version.txt)
- Docker engine: see [docker-version.txt](command-output/docker-version.txt) and [docker-info-safe.txt](command-output/docker-info-safe.txt)
- Docker Compose: see [docker-compose-version.txt](command-output/docker-compose-version.txt)

## Working tree rule

The repository was already modified by the completed Plan 63 implementation
before Plan 64 began. The pre-K0 snapshot is preserved in
[baseline-pre-k0.status](baseline-pre-k0.status). The current status is in
[git-status-current.txt](command-output/git-status-current.txt). K0 does not
silently treat either state as clean.

## Test-data and evidence policy

- Use synthetic users, synthetic money, mock vendors, and disposable local
  volumes only.
- Record configuration names, not secret values.
- Do not archive .env files, private keys, tokens, database dumps, or raw user data.
- Normalized Compose output is redacted before it is committed.
- This evidence authorizes no Kubernetes or cloud mutation.

## Reproduction

~~~sh
make k0-inventory
make k0-inventory-check
~~~

Source hierarchy: running behavior and tests, application code, Dockerfile and
Compose, API contracts, configuration validation, current docs, then archived
roadmap prose.
EOF

printf 'K0 inventory evidence generated at %s\n' "$EVIDENCE_DIR"
