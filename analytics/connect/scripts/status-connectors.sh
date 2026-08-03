#!/usr/bin/env sh
set -eu

connect_url=${ANALYTICS_CONNECT_URL:-http://127.0.0.1:18083}
command -v curl >/dev/null 2>&1 || { echo "status-connectors: curl is required" >&2; exit 2; }
curl --fail-with-body --silent --show-error "$connect_url/connectors?expand=info&expand=status" \
  | jq -r 'to_entries[] | [.key, .value.status.connector.state, (.value.status.tasks[0].state // "none")] | @tsv'
