#!/usr/bin/env bash
# Repo-local supply-chain check (docs/plan/44 K2): every external `uses:` in
# .github/workflows/*.yml must be pinned to a full 40-hex commit SHA, with a
# `# vX.Y.Z` comment recording the human-readable version. A floating tag
# (`@v4`, `@main`) or a short SHA is not an immutable reference — this is
# the repo-local enforcement of that rule (GitHub does not enforce it for
# us: `sha_pinning_required` is false on this repo/org, confirmed T0).
#
# Local actions (`./`) and docker://-style `uses:` are skipped — this check
# is only about EXTERNAL action supply-chain pinning.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FAILED=0

shopt -s nullglob
for f in "$ROOT_DIR"/.github/workflows/*.yml; do
	line_no=0
	while IFS= read -r line; do
		line_no=$((line_no + 1))
		# Only lines with a "uses:" key pointing at owner/repo@ref.
		[[ "$line" =~ uses:[[:space:]]*([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([A-Za-z0-9._/-]+)(.*)$ ]] || continue
		repo="${BASH_REMATCH[1]}"
		ref="${BASH_REMATCH[2]}"
		rest="${BASH_REMATCH[3]}"

		if ! [[ "$ref" =~ ^[0-9a-f]{40}$ ]]; then
			echo "::error file=${f#"$ROOT_DIR"/},line=$line_no::uses '$repo@$ref' is not pinned to a full 40-char commit SHA"
			FAILED=1
			continue
		fi
		if ! [[ "$rest" =~ \#[[:space:]]*v[0-9]+(\.[0-9]+){0,2} ]]; then
			echo "::error file=${f#"$ROOT_DIR"/},line=$line_no::uses '$repo@$ref' is missing a '# vX.Y.Z' version comment"
			FAILED=1
		fi
	done <"$f"
done

if [ "$FAILED" -eq 0 ]; then
	echo "check-action-pins: all external actions are pinned to a full SHA with a version comment"
fi
exit $FAILED
