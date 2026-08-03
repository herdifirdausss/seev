#!/usr/bin/env sh
set -eu

connect_url=${ANALYTICS_CONNECT_URL:-http://127.0.0.1:18083}
clickhouse_url=${ANALYTICS_CLICKHOUSE_URL:-http://127.0.0.1:8123}

curl --fail --silent --show-error "$connect_url/connectors" >/dev/null
curl --fail --silent --show-error "$clickhouse_url/ping" | grep -q '^Ok$'
printf 'analytics core endpoints are healthy\n'
