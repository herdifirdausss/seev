#!/usr/bin/env bash
# Build a safe, deterministic evidence bundle from one or more retained
# verification work directories.
#
# Usage:
#   package-critical-failure-evidence.sh OUTPUT_DIR LABEL SOURCE_DIR [LABEL SOURCE_DIR ...]
#
# Only top-level *.log and *.txt files are copied from source directories.
# This intentionally excludes binaries, pid files, generated certificates,
# encrypted object-store contents, and any future nested directories unless
# the allowlist is changed deliberately.
set -euo pipefail

if [ "$#" -lt 1 ] || [ $((($# - 1) % 2)) -ne 0 ]; then
	printf 'usage: %s OUTPUT_DIR [LABEL SOURCE_DIR]...\n' "${0##*/}" >&2
	exit 2
fi

output_dir="$1"
shift
mkdir -p "$output_dir"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"$script_dir/write-critical-failure-manifest.sh" "$output_dir"

cat >"$output_dir/README.txt" <<'EOF'
This is a Seev critical-failure evidence bundle.

The bundle contains the run manifest and only top-level .log and .txt files
from each selected verification work directory. Generated binaries, process
IDs, certificates/private keys, encrypted object-store contents, and nested
directories are intentionally excluded from the upload allowlist.
EOF

copy_source() {
	local label="$1"
	local source_dir="$2"
	local destination="$output_dir/$label"
	local copied=0
	local file

	mkdir -p "$destination"
	if [ ! -d "$source_dir" ]; then
		printf 'source_status=missing\n' >"$destination/source-status.txt"
		return 0
	fi

	while IFS= read -r -d '' file; do
		cp -- "$file" "$destination/$(basename "$file")"
		copied=1
	done < <(
		find "$source_dir" -mindepth 1 -maxdepth 1 -type f \
			\( -name '*.log' -o -name '*.txt' \) -print0 | sort -z
	)

	if [ "$copied" -eq 1 ]; then
		printf 'source_status=collected\n' >"$destination/source-status.txt"
	else
		printf 'source_status=present-but-no-allowlisted-files\n' >"$destination/source-status.txt"
	fi
}

while [ "$#" -gt 0 ]; do
	label="$1"
	source_dir="$2"
	shift 2
	copy_source "$label" "$source_dir"
done
