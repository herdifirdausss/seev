#!/usr/bin/env bash
set -euo pipefail

SCENARIO="${LOAD_SCENARIO:-}"
RATE="${LOAD_RATE:-}"
[[ -n "$SCENARIO" ]] || { echo "load-capacity: set LOAD_SCENARIO" >&2; exit 2; }
[[ -n "$RATE" ]] || { echo "load-capacity: set LOAD_RATE in WU/s" >&2; exit 2; }
[[ "$SCENARIO" =~ ^(W[1-7]|smoke\.js|scenarios/[a-z0-9-]+\.js)$ ]] || {
	echo "load-capacity: unsupported LOAD_SCENARIO: $SCENARIO" >&2
	exit 2
}
[[ "$RATE" =~ ^[0-9]+([.][0-9]+)?$ ]] && ! [[ "$RATE" =~ ^0+([.]0+)?$ ]] || {
	echo "load-capacity: LOAD_RATE must be a positive number" >&2
	exit 2
}

export SEEV_LOAD_ACK=disposable-only
export SEEV_LOAD_SCENARIO="$SCENARIO"
export SEEV_LOAD_WORKLOAD="$SCENARIO"
export SEEV_LOAD_RATE="$RATE"
exec ./scripts/load-test.sh run
