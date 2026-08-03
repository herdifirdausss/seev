#!/usr/bin/env bash
# Probe only declared local endpoints and broker topology. This helper never
# calls a real vendor and does not claim that Docker bridge addresses are cloud
# callback CIDRs.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/docs/evidence/k0/network}"
APP_HOST="${APP_HOST:-127.0.0.1}"
mkdir -p "$OUTPUT_DIR"

for command in docker curl date; do
  command -v "$command" >/dev/null || { echo "deployment-network-probe: $command is required" >&2; exit 2; }
done

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
output="$OUTPUT_DIR/probe-${stamp}.txt"
{
  printf 'captured_utc=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'policy=local endpoints only; no real vendor calls\n'
  printf '\n[compose containers]\n'
  docker compose --profile app ps -a | sed -E 's/[[:blank:]]+$//' || true

  printf '\n[declared local HTTP health probes]\n'
  for endpoint in \
    "http://${APP_HOST}:8080/health" \
    "http://${APP_HOST}:8082/health" \
    "http://${APP_HOST}:8090/health" \
    "http://${APP_HOST}:8098/health"; do
    code="$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' --connect-timeout 3 "$endpoint" 2>/dev/null || printf 'unreachable')"
    printf '%s %s\n' "$code" "$endpoint"
  done

  printf '\n[declared local data ports]\n'
  for endpoint in \
    "${APP_HOST}:5433" \
    "${APP_HOST}:6380" \
    "${APP_HOST}:5672"; do
    if curl --noproxy '*' -sS --connect-timeout 2 "http://${endpoint}" >/dev/null 2>&1; then
      result=unexpected-http-response
    else
      result=not-http
    fi
    printf '%s %s\n' "$result" "$endpoint"
  done

  printf '\n[rabbitmq topology if the local broker is running]\n'
  broker="$(docker compose ps -q rabbitmq 2>/dev/null || true)"
  if [ -n "$broker" ]; then
    docker exec "$broker" rabbitmqctl list_exchanges name type durable 2>/dev/null || true
    docker exec "$broker" rabbitmqctl list_queues name durable messages consumers arguments 2>/dev/null || true
    docker exec "$broker" rabbitmqctl list_bindings 2>/dev/null || true
  else
    printf 'rabbitmq=not-running\n'
  fi
} >"$output"

printf '%s\n' "$output"
