#!/usr/bin/env sh
set -eu

: "${ANALYTICS_CONFIRM_RESET:?set ANALYTICS_CONFIRM_RESET=analytics-only to remove analytical volumes}"
[ "$ANALYTICS_CONFIRM_RESET" = analytics-only ] || { echo "reset confirmation mismatch" >&2; exit 2; }

docker compose -f analytics/compose/docker-compose.analytics.yml \
  --profile analytics-core --profile analytics-ui --profile analytics-ops down -v --remove-orphans
echo 'analytics Compose state removed; application databases and replication slots were not targeted'
