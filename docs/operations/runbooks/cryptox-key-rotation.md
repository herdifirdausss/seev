# Runbook: pkg/cryptox Key Rotation

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: operators.** Follow this procedure only in an
> environment where you are authorized to change encryption key material.

Covers rotating the shared, cluster-wide `pkg/cryptox` KEK ring (docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
K2/K3/T2.2) — the versioned AES-256 key set every service that encrypts a
sensitive field (auth email/full name/KYC payload/KYC documents, pay-in raw
webhooks, payout destinations, ledger reconciliation raw data, admin BFF
operator identity) reads from. One physical key set is shared cluster-wide
by design — the same deliberate choice this repo already made for
`JWT_SECRET`/`INTERNAL_GRPC_TOKEN` (`scripts/vault-seed.sh`'s own comment)
— field-level isolation comes from each ciphertext's own AAD binding
(service/table/column/row ID), not from separate key material per service.

Unlike [cert-rotation.md](cert-rotation.md), this is **not** a hot-reload:
`pkg/cryptox.Ring` is constructed once at process boot from
`internal/config.CryptoxConfig`, and rotating a key requires a config
change plus a restart of every service that constructed one — there is no
poll-based reload here, because unlike a TLS cert, silently swapping key
material live under in-flight encrypt/decrypt calls has no safe analogue.

## When to run this

- Scheduled: as part of a normal key-hygiene cadence (this repo has no
  fixed TTL on cryptox keys the way certs have a 72h leaf TTL — rotate on
  whatever cadence your own compliance/security policy requires).
- Ad-hoc: after a suspected key compromise, or before decommissioning an
  environment whose key material must not be reused elsewhere.

## Prerequisites

- Write access to wherever `CRYPTOX_KEY_V<N>` values are sourced from in
  the target environment: `deploy/cryptox/secrets/` files (`make
  cryptox-secret`, dev/local), Vault KV v2 (`secret/<service>` via
  `scripts/vault-seed.sh`, dev-mode), or your production secrets manager.
- The ability to restart every service that encrypts a sensitive field —
  today: `auth-service`, `payin-service`, `payout-service`,
  `ledger-service`, `admin-bff-service`.

## Step 1 — Understand the rotation model (K3: expand/backfill/contract)

A `pkg/cryptox.Ring` holds every key version a service was given, but only
ever **writes** under `CurrentVersion`. Every version in the ring —
including retired ones — stays available for `Open`, so a row encrypted
under an older version keeps decrypting after rotation. This means
rotation is inherently gradual:

1. **Introduce the new version** — add it to the ring alongside the old
   one, without changing `CurrentVersion` yet (§2 below). Every service
   restarts able to decrypt both versions, still writing under the old one.
2. **Cut writes over** — bump `CRYPTOX_KEY_CURRENT_VERSION` and restart
   again. New writes use the new version; old rows are untouched and still
   decrypt fine (`pkg/cryptox.Ring.Open` reads the version from each
   envelope's own header, never from `CurrentVersion`).
3. **Backfill (optional, only if you need every row re-encrypted under the
   new version)** — a service-owned re-encrypt-in-place job: read each row
   under its recorded version, re-`Seal` under current, write back. No such
   job exists in this codebase yet; K3's backfill machinery
   (docs/roadmap/active/51 T2.5) is what a real migration reuses for this, the same
   restartable-keyset-batch approach used for the original plaintext→ciphertext
   migration.
4. **Retire the old version** — only once nothing still references it (no
   un-backfilled row, confirmed by a query against every table's own
   key-version column — docs/roadmap/active/51 T2.5's own verification step). Removing
   a version from the ring while any row still needs it makes that row
   permanently undecryptable — there is no recovery path short of an A7
   backup restore predating the removal.

**Never skip straight to retiring the old version** without confirming
step 4's condition — that is the one truly destructive mistake this
procedure can make.

## Step 2 — Generate the new key version

```bash
openssl rand -hex 32
```

Store it as `CRYPTOX_KEY_V<N>` where `<N>` is one higher than the current
maximum version in use (check `CRYPTOX_KEY_CURRENT_VERSION` and every
`CRYPTOX_KEY_V*` already set in the target environment first — never reuse
a version number). Dev/local:

```bash
# deploy/cryptox/secrets/cryptox_key_v<N> — same convention `make cryptox-secret`
# uses for v1; not automated for N>1 since a real rotation needs deliberate
# version-number bookkeeping, not a blind regenerate.
openssl rand -hex 32 > deploy/cryptox/secrets/cryptox_key_v2
chmod 644 deploy/cryptox/secrets/cryptox_key_v2
```

Wire it into `docker-compose.yml` for every affected service (mirroring
`cryptox_key_v1`'s own `secrets:` entry and
`CRYPTOX_KEY_V1_FILE: /run/secrets/cryptox_key_v1` env var — add a
`cryptox_key_v2` secret and `CRYPTOX_KEY_V2_FILE` env var the same way),
or via Vault (extend `scripts/vault-seed.sh`'s `seed_service` to write an
additional `CRYPTOX_KEY_V2` field into each service's KV v2 secret,
alongside the existing `JWT_SECRET`/`INTERNAL_GRPC_TOKEN` pattern).

## Step 3 — Roll out (expand)

Restart every affected service with the new version present but
`CRYPTOX_KEY_CURRENT_VERSION` still unchanged. Confirm each one booted
cleanly — `internal/config.validate`'s production check
(`CRYPTOX_KEY_V<current> is required in production`) only checks the
*current* version is present, so this step alone does not yet require
anything from the new key beyond it being syntactically valid hex.

## Step 4 — Cut over (contract the write path)

Set `CRYPTOX_KEY_CURRENT_VERSION=<N>` in every affected service's
environment and restart again. From this point, every new `Seal` call
uses the new version. Verify:

```bash
go run ./cmd/... # or curl an authenticated request that triggers a write
```

Confirm via `seev_cryptox_seal_total{key_version="<N>",result="ok"}`
(`pkg/cryptox/metrics.go`, K13) that new writes are landing under the new
version. `seev_cryptox_open_total{key_version="<old>",result="ok"}`
continuing to increment is expected and healthy — it means old rows are
still decrypting correctly under the retired version, exactly as designed.

## Step 5 — Retire the old version (only once nothing references it)

Do not remove `CRYPTOX_KEY_V<old>` from any service's environment until a
direct query confirms no row anywhere still carries that version (per
Step 1.4 and docs/roadmap/active/51 T2.5's own verification gate). Once confirmed, remove
the old version's env var/secret file/Vault field from every service and
restart once more — `pkg/cryptox.NewRing` only requires `CurrentVersion` be
present, so dropping a retired version is safe once nothing needs it.

## If something goes wrong

- **A service fails to boot after Step 4** — `internal/config.CryptoxConfig.Ring`
  surfaces both hex-decode errors and `pkg/cryptox.NewRing`'s own
  validation (wrong key length, current version absent) as a boot-time
  config error, never a silent fallback. Fix the flagged env var and
  restart; nothing partially applies.
- **`ErrKeyVersionUnavailable` appears in logs/metrics after Step 5** —
  a row referencing the version you just retired was missed by the
  verification gate. Restore that key version immediately (from wherever
  it was backed up before removal — never regenerate a "new" version under
  the same old version number, since that key material is gone for good)
  and re-run Step 1.4's verification query before retrying retirement.
- **`ErrInvalidEnvelope` on a row that should decrypt fine** — this is
  `pkg/cryptox`'s AAD binding doing its job: the row's service/table/
  column/ID context at read time doesn't match what it was sealed with.
  This is a data-integrity signal (a moved/corrupted row), not a rotation
  bug — do not "fix" it by disabling AAD checking, there is no such
  option.

## Related

- [pkg/cryptox](../../../pkg/cryptox) — the envelope/ring/lookup-key implementation and its own test suite (round-trip, wrong key, wrong AAD, copied ciphertext, truncated envelope, old-key-read/new-key-write).
- [internal/config/config.go](../../../internal/config/config.go) — `CryptoxConfig`, `loadCryptoxKeys`, and the production fail-fast check.
- [Makefile](../../../Makefile) — `cryptox-secret` target.
- [vault-seed.md](vault-seed.md) — the Vault-backed alternative to file-based dev secrets.
- [compliance-a4.md](compliance-a4.md) — what an unconfigured/misconfigured key ring looks like from the KYC-document-upload side.
