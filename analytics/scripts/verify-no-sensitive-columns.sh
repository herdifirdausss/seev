#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root_dir"

go run ./analytics/reconciliation/cmd/validate -root analytics

if rg -n -i 'raw_payload|raw_request|raw_response|destination_ciphertext|account_number|password_hash|refresh_token|private_key|authorization' \
  analytics/connect/connectors analytics/dbt/models analytics/clickhouse/migrations; then
  echo 'sensitive-column scan failed' >&2
  exit 1
fi

echo 'analytics connector and warehouse paths contain no prohibited sensitive columns'
