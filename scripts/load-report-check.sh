#!/usr/bin/env bash
set -euo pipefail

RUNS="${LOAD_RUNS:-}"
OUTPUT="${LOAD_REPORT_OUT:-}"
[[ -n "$RUNS" ]] || { echo "load-report-check: set LOAD_RUNS=path1.json,path2.json" >&2; exit 2; }

args=(-runs "$RUNS")
if [[ -n "$OUTPUT" ]]; then
	args+=(-out "$OUTPUT")
fi
exec go run ./cmd/loadreport "${args[@]}"
