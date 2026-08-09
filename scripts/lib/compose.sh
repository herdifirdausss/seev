#!/usr/bin/env bash
# Compose project/container resolution and container health helpers.
# Sourced by scripts/lib.sh. Never executed directly.

resolve_compose_containers() {
	local resolved
	if [ "${POSTGRES_CONTAINER_EXPLICIT:-0}" -eq 0 ]; then
		resolved="$(docker compose ps -q postgres 2>/dev/null || true)"
		[ -n "$resolved" ] && POSTGRES_CONTAINER="$resolved"
	fi
	if [ "${REDIS_CONTAINER_EXPLICIT:-0}" -eq 0 ]; then
		resolved="$(docker compose ps -q redis 2>/dev/null || true)"
		[ -n "$resolved" ] && REDIS_CONTAINER="$resolved"
	fi
	if [ "${RABBITMQ_CONTAINER_EXPLICIT:-0}" -eq 0 ]; then
		resolved="$(docker compose ps -q rabbitmq 2>/dev/null || true)"
		[ -n "$resolved" ] && RABBITMQ_CONTAINER="$resolved"
	fi
}

wait_for_container_healthy() {
	local container=$1 tries=30
	while [ "$tries" -gt 0 ]; do
		local status
		status="$(docker inspect "$container" --format '{{.State.Health.Status}}' 2>/dev/null || echo "missing")"
		[ "$status" = "healthy" ] && return 0
		sleep 2
		tries=$((tries - 1))
	done
	fail "$container did not become healthy in time"
	return 1
}

wait_for_container_restart() {
	local container=$1 tries=60 transitioned=0
	while [ "$tries" -gt 0 ]; do
		local status
		status="$(docker inspect "$container" --format '{{.State.Health.Status}}' 2>/dev/null || echo "missing")"
		if [ "$status" != "healthy" ]; then
			transitioned=1
			break
		fi
		sleep 0.2
		tries=$((tries - 1))
	done
	if [ "$transitioned" -ne 1 ]; then
		fail "$container restart did not leave the healthy state"
		return 1
	fi
	wait_for_container_healthy "$container"
}
