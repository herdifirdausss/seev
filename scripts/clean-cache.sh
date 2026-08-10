#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin_dir="$root_dir/bin"
terraform_root="$root_dir/deploy/terraform"

go clean -testcache

if [[ "$bin_dir" != "$root_dir/bin" || "$bin_dir" == / || -z "$bin_dir" ]]; then
	printf 'clean-cache: refusing unsafe bin target %s\n' "$bin_dir" >&2
	exit 2
fi
rm -rf -- "$bin_dir"

if [[ -d "$terraform_root" ]]; then
	while IFS= read -r -d '' target; do
		case "$target" in
			"$terraform_root"/.terraform | "$terraform_root"/*/.terraform)
				rm -rf -- "$target"
				;;
			*)
				printf 'clean-cache: refusing unexpected Terraform target %s\n' "$target" >&2
				exit 2
				;;
		esac
	done < <(find "$terraform_root" -type d -name .terraform -prune -print0)
fi

docker_status="unavailable"
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
	docker_status="cleaned"
	if [[ -n "$(docker ps -q)" ]]; then
		docker_status="skipped-active-containers"
	else
		stale_testcontainers=()
		while IFS= read -r container_id; do
			if [[ -n "$container_id" ]]; then
				stale_testcontainers+=("$container_id")
			fi
		done < <(docker ps -aq --filter label=org.testcontainers=true)
		if [[ "${#stale_testcontainers[@]}" -gt 0 ]]; then
			docker rm "${stale_testcontainers[@]}" >/dev/null
		fi
		docker image prune -f >/dev/null
		docker builder prune -f --filter until=24h >/dev/null
	fi
fi

printf 'clean-cache: removed Go test cache, bin/, and deploy/terraform/**/.terraform; docker=%s; volumes untouched\n' "$docker_status"
