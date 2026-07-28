#!/usr/bin/env bash
set -euo pipefail

KIND="${LOAD_SEED_KIND:-journey}"
COUNT="${LOAD_SEED_COUNT:-100}"
OUTPUT="${LOAD_SEED_OUTPUT:-artifacts/load/seed/seed.jsonl}"
ACK="${SEEV_LOAD_ACK:-}"

[[ "$ACK" == "disposable-only" ]] || { echo "load-seed: set SEEV_LOAD_ACK=disposable-only" >&2; exit 2; }
case "$KIND" in
	journey|ledger-size) ;;
	*) echo "load-seed: unsupported LOAD_SEED_KIND: $KIND" >&2; exit 2 ;;
esac
[[ "$COUNT" =~ ^[0-9]+$ && "$COUNT" -ge 1 && "$COUNT" -le 5000000 ]] || {
	echo "load-seed: LOAD_SEED_COUNT must be between 1 and 5000000" >&2
	exit 2
}
[[ "$OUTPUT" == artifacts/load/* || "$OUTPUT" == /tmp/* ]] || {
	echo "load-seed: output must be under artifacts/load or /tmp" >&2
	exit 2
}
[[ "$OUTPUT" != *..* ]] || {
	echo "load-seed: output path must not contain parent-directory traversal" >&2
	exit 2
}

exec go run ./cmd/loadseed -kind "$KIND" -count "$COUNT" -out "$OUTPUT" -ack "$ACK"
