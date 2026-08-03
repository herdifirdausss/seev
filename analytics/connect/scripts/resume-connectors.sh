#!/usr/bin/env sh
set -eu

connect_url=${ANALYTICS_CONNECT_URL:-http://127.0.0.1:18083}
command -v curl >/dev/null 2>&1 || { echo "resume-connectors: curl is required" >&2; exit 2; }

for name in seev-ledger-postgres-cdc seev-payin-postgres-cdc seev-payout-postgres-cdc; do
  curl --fail-with-body --silent --show-error -X PUT "$connect_url/connectors/$name/resume" >/dev/null
  printf '%s resumed\n' "$name"
done
