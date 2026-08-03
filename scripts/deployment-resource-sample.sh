#!/usr/bin/env bash
# Bounded Docker resource sampler for K0 evidence. It never starts, stops, or
# mutates containers; the caller controls which disposable Compose project is
# running.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/docs/evidence/k0/resources}"
DURATION_SECONDS="${DURATION_SECONDS:-60}"
INTERVAL_SECONDS="${INTERVAL_SECONDS:-5}"
PROFILE="${PROFILE:-unspecified}"
COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-}"

for command in docker date awk; do
  command -v "$command" >/dev/null || { echo "deployment-resource-sample: $command is required" >&2; exit 2; }
done
[[ "$DURATION_SECONDS" =~ ^[0-9]+$ && "$DURATION_SECONDS" -ge 1 && "$DURATION_SECONDS" -le 900 ]] || {
  echo "DURATION_SECONDS must be between 1 and 900" >&2
  exit 2
}
[[ "$INTERVAL_SECONDS" =~ ^[0-9]+$ && "$INTERVAL_SECONDS" -ge 1 && "$INTERVAL_SECONDS" -le 60 ]] || {
  echo "INTERVAL_SECONDS must be between 1 and 60" >&2
  exit 2
}

mkdir -p "$OUTPUT_DIR"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
output="$OUTPUT_DIR/${PROFILE}-${stamp}.csv"
printf 'timestamp_utc,profile,container,cpu_percent,memory_usage,memory_limit,memory_percent,net_io,block_io,pids\n' >"$output"

stats_args=()
if [ -n "$COMPOSE_PROJECT_NAME" ]; then
  compose_ids="$(docker compose --profile app ps -q 2>/dev/null || true)"
  if [ -n "$compose_ids" ]; then
    while IFS= read -r container_id; do
      [ -n "$container_id" ] && stats_args+=("$container_id")
    done <<<"$compose_ids"
  fi
elif [ -n "${CONTAINER_IDS:-}" ]; then
  read -r -a stats_args <<<"$CONTAINER_IDS"
fi

started="$(date +%s)"
while :; do
  now="$(date +%s)"
  elapsed=$((now - started))
  docker stats --no-stream "${stats_args[@]}" --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}},{{.NetIO}},{{.BlockIO}},{{.PIDs}}' \
    | while IFS=, read -r container cpu mem_usage mem_percent net_io block_io pids; do
        usage="${mem_usage%% / *}"
        limit="${mem_usage#* / }"
        printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
          "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$PROFILE" "$container" "$cpu" "$usage" "$limit" "$mem_percent" "$net_io" "$block_io" "$pids"
      done >>"$output"
  [ "$elapsed" -ge "$DURATION_SECONDS" ] && break
  sleep "$INTERVAL_SECONDS"
done

printf '%s\n' "$output"
