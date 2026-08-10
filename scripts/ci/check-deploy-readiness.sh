#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
environment="${ENV:-}"
values_input="${VALUES:-}"

if [[ -z "$environment" ]]; then
	printf 'check-deploy-readiness: ENV is required\n' >&2
	exit 2
fi
if [[ -z "$values_input" ]]; then
	values_input="deploy/helm/seev/values-${environment}.yaml"
fi

read -r -a values_files <<<"$values_input"
args=(-env "$environment" -base "$root_dir/deploy/helm/seev/values.yaml")
for values_file in "${values_files[@]}"; do
	if [[ "$values_file" != /* ]]; then
		values_file="$root_dir/$values_file"
	fi
	args+=(-values "$values_file")
done

cd "$root_dir"
go run ./tools/deployreadiness "${args[@]}"
