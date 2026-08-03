#!/usr/bin/env sh
set -eu

exec docker compose -f analytics/compose/docker-compose.analytics.yml \
  --profile analytics-core run --rm dbt dbt "$@"
