#!/usr/bin/env sh
set -eu

compose_file=analytics/compose/docker-compose.analytics.yml
exec docker compose -f "$compose_file" "$@"
