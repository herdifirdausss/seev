#!/usr/bin/env bash
# Full local disaster-recovery E2E. Each mode creates and destroys only its
# isolated game-day Compose project, restores the application databases, and
# proves a post-restore user journey. The default development stack must be
# stopped first; scripts/dr-drill.sh refuses to touch it.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TMP_ROOT="${TMPDIR:-/tmp}"
REPORT_DIR="$(mktemp -d "$TMP_ROOT/seev-dr-e2e.XXXXXX")"
trap 'rm -rf "$REPORT_DIR"' EXIT

for mode in latest pitr; do
	report="$REPORT_DIR/$mode.json"
	printf 'dr-e2e: running %s restore journey\n' "$mode"
	DRILL_REPORT_PATH="$report" "$ROOT_DIR/scripts/dr-drill.sh" "$mode"
	test -s "$report" || {
		printf 'dr-e2e: %s did not produce a report\n' "$mode" >&2
		exit 1
	}
done

printf 'dr-e2e: latest and PITR restore journeys passed with post-restore application smoke\n'
