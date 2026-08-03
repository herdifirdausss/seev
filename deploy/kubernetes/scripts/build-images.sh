#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
REGISTRY="${IMAGE_REPOSITORY:-seev}"
TAG="${IMAGE_TAG:-dev}"
PUSH="false"
if [[ "${1:-}" == "--push" ]]; then
  PUSH=true
elif [[ "${1:-}" != "" ]]; then
  echo "usage: $0 [--push]" >&2
  exit 2
fi

services=(gateway-service auth-service ledger-service payin-service payout-service fraud-service vendor-service admin-bff-service assurance-service)
for service in "${services[@]}"; do
  build_service="$service"
  [[ "$service" == gateway-service ]] && build_service=gateway
  image="$REGISTRY/$service:$TAG"
  docker build --build-arg SERVICE="$build_service" --build-arg REVISION="${GIT_REVISION:-unknown}" -t "$image" "$ROOT_DIR"
  if [[ "$PUSH" == true ]]; then
    docker push "$image"
  fi
done

migration_image="$REGISTRY/migrations:$TAG"
docker build --build-arg REVISION="${GIT_REVISION:-unknown}" -f "$ROOT_DIR/deploy/kubernetes/migrations.Dockerfile" -t "$migration_image" "$ROOT_DIR"
if [[ "$PUSH" == true ]]; then
  docker push "$migration_image"
fi

echo "images built under $REGISTRY with tag $TAG"
docker image inspect $(printf '%s/%s:%s ' "$REGISTRY" "${services[0]}" "$TAG") >/dev/null
