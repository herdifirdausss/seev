#!/usr/bin/env bash
# Validate the capability inventory's shape and evidence references. The
# Integration column is a repository completion gate: every listed capability
# must have a direct repeatable integration path. E2E/chaos, runtime, and
# production acceptance remain separate states and may still be incomplete.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
inventory="$root_dir/docs/engineering/capability-inventory.md"

if [[ ! -s "$inventory" ]]; then
	printf 'check-capability-inventory: missing inventory: %s\n' "$inventory" >&2
	exit 1
fi

required_rows=0
failed=0
trim() {
	local value="$1"
	value="${value#"${value%%[![:space:]]*}"}"
	value="${value%"${value##*[![:space:]]}"}"
	printf '%s' "$value"
}

check_repo_status() {
	case "$1" in
		implemented|partially_implemented|not_started|not_applicable) return 0 ;;
		*) return 1 ;;
	esac
}

check_evidence_paths() {
	local evidence_text="$1"
	local path
	local paths
	# shellcheck disable=SC2016
	paths="$(printf '%s\n' "$evidence_text" | grep -oE '`[^`]+`' | tr -d '`' || true)"
	if [[ -z "$paths" ]]; then
		return 1
	fi
	while IFS= read -r path; do
		[[ -z "$path" ]] && continue
		if [[ ! -e "$root_dir/$path" ]]; then
			printf '::error file=docs/engineering/capability-inventory.md::evidence path does not exist: %s\n' "$path" >&2
			return 1
		fi
	done <<< "$paths"
}

# shellcheck disable=SC2034
while IFS='|' read -r _ capability code schema unit integration e2e chaos runtime production evidence _; do
	capability="$(trim "${capability:-}")"
	[[ -z "$capability" || "$capability" == "Capability" || "$capability" == ---* ]] && continue
	[[ "$capability" == "Status interpretation" ]] && continue

	required_rows=$((required_rows + 1))
	for field in code schema unit integration e2e chaos; do
		value="$(trim "${!field:-}")"
		if ! check_repo_status "$value"; then
			printf '::error file=docs/engineering/capability-inventory.md::%s has invalid %s=%s; expected one of implemented, partially_implemented, not_started, not_applicable\n' "$capability" "$field" "${value:-<empty>}" >&2
			failed=1
		fi
		if [[ "$field" == "integration" && "$value" != "implemented" ]]; then
			printf '::error file=docs/engineering/capability-inventory.md::%s must have Integration=implemented; found %s\n' "$capability" "${value:-<empty>}" >&2
			failed=1
		fi
	done
	runtime="$(trim "${runtime:-}")"
	if [[ "$runtime" != "evidence_required" && "$runtime" != "accepted" ]]; then
		printf '::error file=docs/engineering/capability-inventory.md::%s has invalid runtime accepted status=%s; expected evidence_required or accepted\n' "$capability" "${runtime:-<empty>}" >&2
		failed=1
	fi
	production="$(trim "${production:-}")"
	if [[ "$production" != "evidence_required" && "$production" != "production_ready" ]]; then
		printf '::error file=docs/engineering/capability-inventory.md::%s has invalid production ready status=%s; expected evidence_required or production_ready\n' "$capability" "${production:-<empty>}" >&2
		failed=1
	fi
	if [[ -z "$(trim "${evidence:-}")" ]]; then
		printf '::error file=docs/engineering/capability-inventory.md::%s has no owner/evidence reference\n' "$capability" >&2
		failed=1
	elif ! check_evidence_paths "$evidence"; then
		printf '::error file=docs/engineering/capability-inventory.md::%s has no valid repository evidence path\n' "$capability" >&2
		failed=1
	fi
done < <(awk '/^\|/ { print }' "$inventory")

if [[ "$required_rows" -ne 17 ]]; then
	printf '::error file=docs/engineering/capability-inventory.md::expected 17 capability rows, found %s\n' "$required_rows" >&2
	failed=1
fi

if [[ "$failed" -ne 0 ]]; then
	exit 1
fi
printf 'check-capability-inventory: validated %s capability rows; all Integration paths are implemented and live gates remain explicit\n' "$required_rows"
