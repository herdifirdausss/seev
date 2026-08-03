#!/usr/bin/env sh
set -eu

scenario=${1:-all}
case "$scenario" in
  connect)
    docker compose -f analytics/compose/docker-compose.analytics.yml stop connect
    docker compose -f analytics/compose/docker-compose.analytics.yml start connect
    ;;
  redpanda)
    docker compose -f analytics/compose/docker-compose.analytics.yml stop redpanda
    docker compose -f analytics/compose/docker-compose.analytics.yml start redpanda
    ;;
  clickhouse)
    docker compose -f analytics/compose/docker-compose.analytics.yml stop clickhouse
    docker compose -f analytics/compose/docker-compose.analytics.yml start clickhouse
    ;;
  source)
    echo 'Restart the application postgres service only in a disposable local app project.'
    echo 'The analytics connector must remain running and the OLTP smoke journey must be observed.'
    ;;
  duplicate)
    echo 'Inject the committed duplicate transport fixture through the Redpanda test topic.'
    echo 'Assert raw transport identity may repeat but core logical keys and marts do not.'
    ;;
  schema)
    echo 'Apply the incompatible schema fixture only to a disposable source database.'
    echo 'Assert connector/model failure is visible and no silent default is published.'
    ;;
  all)
    echo 'Run scenarios connect, redpanda, clickhouse, source, duplicate, and schema in a disposable project.'
    ;;
  *)
    echo "usage: $0 [connect|redpanda|clickhouse|source|duplicate|schema|all]" >&2
    exit 2
    ;;
esac
