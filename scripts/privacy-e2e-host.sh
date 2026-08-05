#!/usr/bin/env bash
# Managed host-binary wrapper for scripts/privacy-e2e.sh.
#
# The underlying privacy script intentionally supports an already-running
# stack. This wrapper supplies the same isolated lifecycle as the other host
# journeys so verify-full and CI can run the privacy acceptance gate without
# relying on a developer's leftover processes or volumes.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="privacy-e2e"
LIB_WORK_DIR_PREFIX="privacy-e2e"
# shellcheck source=scripts/lib.sh
source "$ROOT_DIR/scripts/lib.sh"
trap cleanup EXIT

ensure_deps_up
build_server
export ASSURANCE_INTERVAL=1s
export ASSURANCE_CONSISTENCY_DELAY=0s
start_services

set +e
AUTH_URL="http://localhost:$AUTH_APP_PORT" \
	GATEWAY_URL="http://localhost:$APP_PORT" \
	ASSURANCE_URL="https://localhost:$ASSURANCE_PORT" \
	TLS_CERT_DIR="$CERT_DIR" \
	JWT_SECRET="$JWT_SECRET" \
	JWT_ISSUER="$JWT_ISSUER" \
	POSTGRES_HOST=localhost \
	POSTGRES_PORT="$DB_HOST_PORT" \
	POSTGRES_USER=seev_app \
	POSTGRES_PASSWORD=seev_app \
	"$ROOT_DIR/scripts/privacy-e2e.sh" 2>&1 | tee "$WORK_DIR/privacy-e2e.stdout.log"
statuses=("${PIPESTATUS[@]}")
set -e
if [ "${statuses[0]}" -ne 0 ]; then
	exit "${statuses[0]}"
fi
exit "${statuses[1]}"
