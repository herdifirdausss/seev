# Runbook: Vault Dev-Mode Seed

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: local-development operators.** This procedure is
> for the disposable development Vault, not a production secret store.

Covers seeding the local dev-mode Vault (docs/roadmap/archive/49 K7) that `internal/config`'s `vaultGetenv` seam can overlay on top of plain env vars when `VAULT_ADDR`/`VAULT_TOKEN` are set. Dev-mode Vault is **in-memory** — every restart wipes it, and this seed script exists precisely because "reseed on every boot" is the normal, expected operating mode here, not an edge case.

## When to run this

- Every time the `secrets` Compose profile's Vault container is (re)started — dev mode has no persistence, so a fresh container is always empty.
- After adding a new service that should source its `JWT_SECRET`/`INTERNAL_GRPC_TOKEN` from Vault instead of plain env.

## Prerequisites

- `curl` and `openssl` on PATH.
- Vault running: `docker compose --profile secrets up -d vault` (opt-in profile, mirrors the `observability` profile's pattern — not part of the default stack).

## Step 1 — Understand the KV v2 write semantics

**A `POST` to `secret/data/<path>` REPLACES the entire secret at that path — it does not merge.** A second write to the same path with only one key would silently wipe out every other key a prior write put there. This is why `seed_service` in `scripts/vault-seed.sh` builds one JSON body containing every key a service needs and issues exactly one `POST` per service, never one `POST` per key. Keep this in mind before writing any ad-hoc `curl` against Vault directly — always write a service's *complete* secret set in one call.

## Step 2 — Seed

```bash
docker compose --profile secrets up -d vault
./scripts/vault-seed.sh
```

Idempotent: re-running never rotates an already-seeded value (it checks for existing `JWT_SECRET`+`INTERNAL_GRPC_TOKEN` keys first and skips a service that already has both). Since dev-mode storage is in-memory anyway, "idempotent" mostly matters within a single container's lifetime — a fresh container always starts empty regardless.

To point at a non-default Vault:

```bash
VAULT_ADDR=http://localhost:18200 VAULT_TOKEN=<token> ./scripts/vault-seed.sh
```

## Step 3 — What gets seeded, and what deliberately doesn't

Seeded, one shared value written identically into every service's own KV v2 entry (both must agree cluster-wide — issuer/verifier and client/server pairs):

- `JWT_SECRET`
- `INTERNAL_GRPC_TOKEN`
- `AUTH_BOOTSTRAP_ADMIN_PASSWORD` — auth-service only, safe to randomize independently.

**Not seeded, on purpose:**

- `POSTGRES_PASSWORD` — `docker-compose.yml` hardcodes the Postgres role passwords and `scripts/postgres-init` provisions the database with those exact values; a Vault-sourced password would authenticate against a role Postgres was never told to expect.
- Mockvendor webhook secrets — need to stay in sync with whatever signs mock webhooks in the same environment; sourcing them from a third place (Vault) risks a silent mismatch.
- `JWT_ISSUER` — not a secret (docs/roadmap/archive/49 TM-07 made it mandatory, but it's a plain identifier, not sensitive). `vaultGetenv` falls through to the underlying env var for any key Vault doesn't have — confirmed by `TestVaultGetenv_FallsThroughToEnvForKeysVaultDoesNotHave` — so a service booting with `VAULT_ADDR`/`VAULT_TOKEN` set still gets `JWT_ISSUER` from its regular env (`.env.example`'s `JWT_ISSUER=seev` default, or whatever Compose/`scripts/lib.sh` set) without needing a Vault entry at all.

## Step 4 — Consume it

Boot a service with Vault as its config source:

```bash
VAULT_ADDR=http://localhost:18200 VAULT_TOKEN=<token> APP_NAME=<service> ./<service-binary>
```

Precedence is Vault-over-env for whichever specific keys Vault actually has — unset `VAULT_ADDR`/`VAULT_TOKEN` (or a key Vault doesn't carry) falls through to today's plain-env behavior untouched, so nothing about local/CI/nightly boot paths that don't use Vault changes.

## Step 5 — Known residual risk

Dev-mode Vault talks plain HTTP on the same Compose network every other service is on (docs/roadmap/archive/49 K7/TM-10, accepted-risk) — secrets fetched from Vault cross that hop without TLS unless a future mTLS sweep explicitly adds Vault as a known identity (it is not one of K3/K4's identities today). A TLS listener for Vault itself is an explicit follow-up outside doc 49's scope, not something this seed script or runbook can fix.

## Related

- [scripts/vault-seed.sh](../../../scripts/vault-seed.sh) — the script this runbook operates.
- [internal/config/config.go](../../../internal/config/config.go) `vaultGetenv`/`fetchVaultKV` — the consuming seam.
- [docs/roadmap/archive/49-a6-internal-security.md](../../roadmap/archive/49-a6-internal-security.md) K7 — the dev-mode-vs-production-Vault decision and its stated trade-offs.
