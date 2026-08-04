#!/usr/bin/env bash
# Validate the fail-closed deployment contract without printing secret values.
set -euo pipefail

env_name="${APP_ENV:-development}"
if [[ "$env_name" == "development" || "$env_name" == "ci" ]]; then
	printf 'check-environment-contract: %s permits local development defaults\n' "$env_name"
	exit 0
fi

if [[ "$env_name" != "staging" && "$env_name" != "production" ]]; then
	printf 'check-environment-contract: unsupported APP_ENV=%s\n' "$env_name" >&2
	exit 2
fi

failed=0
require_value() {
	local key="$1"
	if [[ -z "${!key:-}" ]]; then
		printf '::error::%s is required for APP_ENV=%s\n' "$key" "$env_name" >&2
		failed=1
	fi
}

for key in POSTGRES_HOST POSTGRES_PORT POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB \
	JWT_SECRET JWT_ISSUER INTERNAL_GRPC_TOKEN TLS_CERT_DIR KYC_PROVIDER_URL \
	KYC_PROVIDER_TOKEN VENDOR_EGRESS_PROXY_URL; do
	require_value "$key"
done

if [[ "${POSTGRES_SSL_MODE:-}" != "require" && "${POSTGRES_SSL_MODE:-}" != "verify-full" ]]; then
	printf '::error::POSTGRES_SSL_MODE must be require or verify-full for APP_ENV=%s\n' "$env_name" >&2
	failed=1
fi

if [[ "${REDIS_ENABLED:-true}" != "true" ]]; then
	printf '::error::REDIS_ENABLED must be true for multi-replica APP_ENV=%s\n' "$env_name" >&2
	failed=1
fi
require_value REDIS_ADDR
require_value RABBITMQ_HOST
require_value RABBITMQ_USERNAME
require_value RABBITMQ_PASSWORD
require_value RABBITMQ_EXCHANGE

if [[ "${VENDOR_EGRESS_PROXY_REQUIRED:-false}" != "true" ]]; then
	printf '::error::VENDOR_EGRESS_PROXY_REQUIRED must be true outside development\n' >&2
	failed=1
fi

for key in VENDOR_MOCKVENDOR_ENABLED MOCKVENDOR2_ENABLED; do
	if [[ "${!key:-false}" == "true" ]]; then
		printf '::error::%s must be false for APP_ENV=%s\n' "$key" "$env_name" >&2
		failed=1
	fi
done

for key in POSTGRES_HOST REDIS_ADDR RABBITMQ_HOST VENDOR_EGRESS_PROXY_URL KYC_PROVIDER_URL; do
	value="${!key:-}"
	if [[ "$value" == *localhost* || "$value" == *127.0.0.1* || "$value" == *"[::1]"* ]]; then
		printf '::error::%s points at loopback for APP_ENV=%s\n' "$key" "$env_name" >&2
		failed=1
	fi
done

if (( failed != 0 )); then
	exit 1
fi
printf 'check-environment-contract: %s passed (secret values were not displayed)\n' "$env_name"
