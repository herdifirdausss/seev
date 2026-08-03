#!/usr/bin/env sh
set -eu

connect_url=${ANALYTICS_CONNECT_URL:-http://127.0.0.1:18083}
source_host=${ANALYTICS_SOURCE_DB_HOST:-host.docker.internal}
source_port=${ANALYTICS_SOURCE_DB_PORT:-5433}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
connector_dir="$script_dir/../connectors"

"$script_dir/validate-connectors.sh" >/dev/null
command -v jq >/dev/null 2>&1 || { echo "apply-connectors: jq is required" >&2; exit 2; }
command -v curl >/dev/null 2>&1 || { echo "apply-connectors: curl is required" >&2; exit 2; }

for file in "$connector_dir"/*.json; do
  name=$(jq -r '.name' "$file")
  case "$name" in
    seev-ledger-postgres-cdc)
      source_user=${ANALYTICS_LEDGER_USER:-seev_analytics_ledger}
      source_password=${ANALYTICS_LEDGER_PASSWORD:?ANALYTICS_LEDGER_PASSWORD is required}
      ;;
    seev-payin-postgres-cdc)
      source_user=${ANALYTICS_PAYIN_USER:-seev_analytics_payin}
      source_password=${ANALYTICS_PAYIN_PASSWORD:?ANALYTICS_PAYIN_PASSWORD is required}
      ;;
    seev-payout-postgres-cdc)
      source_user=${ANALYTICS_PAYOUT_USER:-seev_analytics_payout}
      source_password=${ANALYTICS_PAYOUT_PASSWORD:?ANALYTICS_PAYOUT_PASSWORD is required}
      ;;
    *)
      echo "apply-connectors: unknown connector $name" >&2
      exit 1
      ;;
  esac
  config=$(jq \
    --arg host "$source_host" \
    --arg port "$source_port" \
    --arg user "$source_user" \
    --arg password "$source_password" \
    '.config
      | .["database.hostname"] = $host
      | .["database.port"] = $port
      | .["database.user"] = $user
      | .["database.password"] = $password' "$file")

  curl --fail-with-body --silent --show-error \
    -X PUT "$connect_url/connectors/$name/config" \
    -H 'Content-Type: application/json' \
    --data "$config" >/dev/null
  printf '%s applied\n' "$name"
done
