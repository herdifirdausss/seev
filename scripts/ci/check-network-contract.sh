#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
template="$root_dir/deploy/helm/seev/templates/networkpolicy.yaml"

if grep -qE '^  name: (app-allow-internal-services|app-allow-data)$' "$template"; then
	printf 'network-contract: namespace-wide allow policy remains in %s\n' "$template" >&2
	exit 1
fi

cd "$root_dir"
go run ./tools/networkcontract \
	-values deploy/helm/seev/values.yaml \
	-dependencies deploy/inventory/dependencies.yaml
