#!/usr/bin/env sh
set -eu

connect_url=${ANALYTICS_CONNECT_URL:-http://127.0.0.1:18083}
: "${ANALYTICS_CONFIRM_DELETE:?set ANALYTICS_CONFIRM_DELETE=connectors to delete connector definitions}"
[ "$ANALYTICS_CONFIRM_DELETE" = connectors ] || { echo "delete-connectors: confirmation mismatch" >&2; exit 2; }
command -v curl >/dev/null 2>&1 || { echo "delete-connectors: curl is required" >&2; exit 2; }

for name in seev-ledger-postgres-cdc seev-payin-postgres-cdc seev-payout-postgres-cdc; do
  curl --fail-with-body --silent --show-error -X DELETE "$connect_url/connectors/$name" >/dev/null
  printf '%s deleted; replication slots remain for explicit source cleanup\n' "$name"
done
