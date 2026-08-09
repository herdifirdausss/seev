#!/usr/bin/env bash
# Seeds the local dev-mode Vault (docs/roadmap/archive/49 K7) with the secrets
# internal/platform/config's vaultGetenv seam will overlay on top of env when
# VAULT_ADDR/VAULT_TOKEN are set. Idempotent — re-running never rotates an
# already-seeded value (dev mode's storage is in-memory anyway, so a fresh
# container always starts empty and needs a real seed regardless).
#
# Scope is deliberately narrow: only secrets that are safe to source
# independently of anything else already provisioned. JWT_SECRET,
# INTERNAL_GRPC_TOKEN, and CRYPTOX_KEY_V1 are shared cluster-wide (every
# service must agree on the SAME value — issuer/verifier or client/server
# pairs, or in cryptox's case docs/roadmap/archive/51 K2's own deliberate
# one-key-set-per-cluster design), so each is generated once and written
# identically into every service's own KV v2 entry. CRYPTOX_LOOKUP_KEY is
# also shared but only auth actually reads it (K2's normalized-email HMAC
# digest) — every other service gets it anyway rather than special-casing
# one service out of an otherwise-uniform seed body.
# AUTH_BOOTSTRAP_ADMIN_PASSWORD is auth-service-only and safe to
# randomize independently (nothing else needs to already know it).
# POSTGRES_PASSWORD and the mockvendor webhook secrets are deliberately NOT
# seeded here: docker-compose.yml hardcodes the Postgres role passwords
# (scripts/postgres-init provisions the database with those EXACT values —
# a different Vault-sourced password would authenticate against a role
# Postgres was never told to expect), and the vendor secrets need to stay
# in sync with whatever signs mock webhooks in this same environment.
#
# Usage:
#   docker compose --profile secrets up -d vault
#   ./scripts/vault-seed.sh
#   VAULT_ADDR=http://localhost:18200 VAULT_TOKEN=... ./scripts/vault-seed.sh   # override
#
# Requires: curl, openssl.

set -euo pipefail

VAULT_ADDR="${VAULT_ADDR:-http://localhost:18200}"
VAULT_TOKEN="${VAULT_TOKEN:-seev-dev-root-token}"

log() { printf '\033[1;34m[vault-seed]\033[0m %s\n' "$*"; }

for bin in curl openssl; do
	command -v "$bin" >/dev/null 2>&1 || {
		echo "vault-seed: required dependency '$bin' not found in PATH" >&2
		exit 2
	}
done

log "checking Vault at $VAULT_ADDR..."
if ! curl -fsS -o /dev/null "$VAULT_ADDR/v1/sys/health"; then
	echo "vault-seed: Vault not reachable at $VAULT_ADDR — start it first:" >&2
	echo "  docker compose --profile secrets up -d vault" >&2
	exit 1
fi

SERVICES=(gateway-service auth-service ledger-service payin-service payout-service fraud-service admin-bff-service assurance-service)

# Vault's KV v2 write REPLACES the whole secret at that path — every key a
# service needs must go in ONE POST, never a separate call per key (a
# second write with only one key would silently wipe out whatever the
# first write just wrote). auth-service is the only service with a fifth,
# service-specific key; every other service gets exactly the four shared
# ones.
seed_service() {
	local service=$1 jwt_secret=$2 internal_grpc_token=$3 cryptox_key_v1=$4 cryptox_lookup_key=$5
	local existing
	existing="$(curl -fsS "$VAULT_ADDR/v1/secret/data/$service" -H "X-Vault-Token: $VAULT_TOKEN" 2>/dev/null || true)"

	if echo "$existing" | grep -q '"JWT_SECRET"' && echo "$existing" | grep -q '"INTERNAL_GRPC_TOKEN"' && echo "$existing" | grep -q '"CRYPTOX_KEY_V1"'; then
		log "$service: already seeded, leaving it alone"
		return
	fi

	local body
	if [ "$service" = "auth-service" ]; then
		body="{\"data\":{\"JWT_SECRET\":\"$jwt_secret\",\"INTERNAL_GRPC_TOKEN\":\"$internal_grpc_token\",\"CRYPTOX_KEY_V1\":\"$cryptox_key_v1\",\"CRYPTOX_LOOKUP_KEY\":\"$cryptox_lookup_key\",\"AUTH_BOOTSTRAP_ADMIN_PASSWORD\":\"$(openssl rand -hex 16)\"}}"
	else
		body="{\"data\":{\"JWT_SECRET\":\"$jwt_secret\",\"INTERNAL_GRPC_TOKEN\":\"$internal_grpc_token\",\"CRYPTOX_KEY_V1\":\"$cryptox_key_v1\",\"CRYPTOX_LOOKUP_KEY\":\"$cryptox_lookup_key\"}}"
	fi
	curl -fsS -o /dev/null -X POST "$VAULT_ADDR/v1/secret/data/$service" \
		-H "X-Vault-Token: $VAULT_TOKEN" -H "Content-Type: application/json" \
		-d "$body"
	log "$service: seeded"
}

JWT_SECRET="$(openssl rand -hex 32)"
INTERNAL_GRPC_TOKEN="$(openssl rand -hex 32)"
CRYPTOX_KEY_V1="$(openssl rand -hex 32)"
CRYPTOX_LOOKUP_KEY="$(openssl rand -hex 32)"

for service in "${SERVICES[@]}"; do
	seed_service "$service" "$JWT_SECRET" "$INTERNAL_GRPC_TOKEN" "$CRYPTOX_KEY_V1" "$CRYPTOX_LOOKUP_KEY"
done

log "done. Boot any service with VAULT_ADDR=$VAULT_ADDR VAULT_TOKEN=<token> APP_NAME=<service> to source these."
