#!/usr/bin/env bash
# Validate real-provider staging wiring without printing credentials.
set -euo pipefail

env_name="${APP_ENV:-development}"
if [[ "$env_name" == "development" || "$env_name" == "ci" ]]; then
	printf 'check-vendor-sandbox-config: %s uses deterministic fixtures\n' "$env_name"
	exit 0
fi

failed=0
for key in VENDOR_SERVICE_ENABLED VENDOR_EGRESS_PROXY_REQUIRED VENDOR_EGRESS_PROXY_URL; do
	if [[ -z "${!key:-}" ]]; then
		printf '::error::%s is required for a non-local vendor environment\n' "$key" >&2
		failed=1
	fi
done

if [[ "${VENDOR_SERVICE_ENABLED:-false}" != "true" || "${VENDOR_EGRESS_PROXY_REQUIRED:-false}" != "true" ]]; then
	printf '::error::real vendor sandbox requires VENDOR_SERVICE_ENABLED=true and VENDOR_EGRESS_PROXY_REQUIRED=true\n' >&2
	failed=1
fi

for key in VENDOR_MOCKVENDOR_ENABLED MOCKVENDOR2_ENABLED; do
	if [[ "${!key:-false}" == "true" ]]; then
		printf '::error::mock adapter %s is forbidden in %s\n' "$key" "$env_name" >&2
		failed=1
	fi
done

if [[ "${VENDOR_EGRESS_PROXY_URL:-}" == *localhost* || "${VENDOR_EGRESS_PROXY_URL:-}" == *127.0.0.1* ]]; then
	printf '::error::VENDOR_EGRESS_PROXY_URL must be a private non-loopback endpoint\n' >&2
	failed=1
fi

if (( failed != 0 )); then
	exit 1
fi
printf 'check-vendor-sandbox-config: %s provider wiring passed (credentials were not displayed)\n' "$env_name"
