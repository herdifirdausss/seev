# 46 — Track A4: Advanced Compliance

Status: T1–T7 complete (2026-07-20). The track adds KYC retry and downgrade, per-rule screening modes, durable screening events, local sanctions data, encrypted documents, periodic re-screening, an HTTP KYC sandbox adapter, and compliance observability. Production MinIO and provider credentials remain configuration-gated.

## Trigger and goals

Prerequisites [plan 39](39-phase7d-kyc-tiers.md), [plan 37](37-phase7b-fraud-seam.md), and [plan 20](20-phase3d-aml-reporting.md) are complete. The learning decision is to use a local OpenSanctions dataset, short JWT TTL plus hard policy controls, and a provider contract that can be connected to a real sandbox without making it part of CI.

Business goals:

- recover KYC tier application without manual re-triggering;
- support safe, audited downgrade;
- change screening rules without deployment;
- preserve screening events durably and measure residual loss;
- screen sanctions at KYC time and during periodic re-screening;
- keep identity documents encrypted at rest.

## Anti-scope

- no legal compliance certification or licensing claim;
- no full case-management UI (admin BFF is plan 47);
- no provider credentials or external downloads in CI;
- no change to `execTransfer`, RLS, AMQP, or the fail-open fraud client contract;
- no production KMS/HSM; document KEK rotation belongs to internal-security work;
- no automatic sanctions-driven downgrade.

## Locked decisions

### K1 — Limits-first KYC retry queue

When `ApplyKycTier` fails, keep the submission pending and write a durable `kyc_apply_retries` intent in auth after the original transaction rolls back. A scheduler worker claims intents with `FOR UPDATE SKIP LOCKED`, retries with backoff and jitter, and marks permanently failing intents dead with an alert. The existing inline approval path remains the fast path. Limits must be applied before the user level advances.

### K2 — Admin downgrade

Admin downgrade requires a reason. Apply the lower ledger limits first, then lower `auth_users.kyc_level`, and record every upgrade or downgrade in `kyc_level_changes`. Add L0 policy templates with zero limits so the hard policy control works even while an old JWT still claims a higher level. Pending submissions remain pending.

### K3 — JWT staleness

Set the default access-token TTL to five minutes. JWT gates are UX controls; policy limits are authoritative. Document the accepted maximum staleness as token TTL plus the policy cache TTL rather than adding cross-service cache invalidation.

### K4 — Per-rule screening mode

Add `screening_rule_modes` with `off`, `monitor`, and `block`, updated-by metadata, RLS, and a short cache. Missing rows fall back to the global `SCREENING_MODE`; an off rule remains registered but performs a fast no-op. Add admin GET/PUT endpoints and audit the administrator.

### K5 — Durable screening events

Rules return verdicts and event data; `Module.Screen` writes events centrally. On database failure, queue events in a bounded in-memory spill buffer and flush with retries. Overflow drops the oldest event with explicit counters. A blocked verdict remains a block even if audit persistence fails. Crash loss of a non-durable spill buffer is documented and measured.

### K6 — Local sanctions screening

Add an optional subject name and birth date to the fraud request. Load a local OpenSanctions export into `sanctions_entries`, normalize names, and use conservative exact/token-sorted matching. Screen at KYC submission and periodically re-screen approved users. Monitor mode flags; block mode rejects the submission. Tests use a committed local fixture, not a network download.

### K7 — KYC provider contract

Keep the existing provider interface and add a config-gated HTTP adapter with explicit timeout, verdict validation, reference IDs, and async result mapping. The implementation must be usable with a sandbox but remain disabled by default and absent from CI without credentials.

### K8 — Encrypted documents and observability

Use an optional `kycstore` Compose profile. Encrypt each document with a random AES-GCM data key wrapped by an environment-provided key-encryption key. Store only object key, plaintext hash, size, and MIME metadata. Cap uploads, allowlist MIME types, audit admin downloads, and return 503 when storage is unavailable without breaking JSON KYC. Add queue, spill, sanctions, dashboard, and alert metrics.

## Execution tasks and results

### T1 — Retry queue

Add auth migration 000003, retry repository, lease worker, dead-letter metrics, and queued approval semantics. **Result:** real auth/ledger integration and full verification prove failed tier application is retried automatically while the limits-first invariant remains intact.

### T2 — Downgrade and L0

Add ledger migration 000023, L0 zero-limit templates, admin downgrade, audit trail, and five-minute JWT default. **Result:** integration tests prove a stale L1 token cannot bypass L0 hard policy limits; downgrade and re-upgrade are idempotent.

### T3 — Rule modes

Add fraud migration 000003, cached per-rule resolver, admin CRUD, and environment fallback. **Result:** switching monitor/block/off works without restarting fraud-service and records who changed the rule.

### T4 — Durable event persistence

Centralize writes, add bounded FIFO spill and flush worker, and expose write-failure, depth, and loss metrics. **Result:** unit and recovery tests prove verdicts remain correct, events flush after database recovery, and overflow is measurable.

### T5 — Sanctions dataset and re-screening

Add fraud migration 000004, offline loader, normalized matching rule, KYC-time screening, and incremental re-screen worker. **Result:** local fixtures cover match/no-match and monitor/block behavior; no CI network access or automatic downgrade is used.

### T6 — Documents and provider adapter

Add optional encrypted document storage and a config-gated HTTP KYC sandbox contract. **Result:** encryption round trips, wrong-key failures, MIME/size validation, storage-off behavior, and deterministic `httptest` provider behavior pass. Default builds require neither MinIO nor provider credentials.

### T7 — Chaos, observability, and documentation

Add KYC approval recovery chaos, dashboards, alerts, and the compliance runbook. **Result:** clean-volume full verification, admin E2E, provider contract tests, and chaos scenarios 1–14 pass. The plan index and future-work documentation were updated.

## Constraints

Preserve limits-first ordering, the existing fraud client contract, `scripts/lib.sh` lifecycle, RLS, and low-cardinality metrics. Verify current external dataset and provider facts at execution time. Never log names, sanctions payloads, plaintext documents, KEKs, or credentials. Reset volumes before each full gate and do not combine kycstore, observability, and testcontainers on the development machine.

## Definition of done

- [x] Retry and downgrade are self-healing and limits-first.
- [x] Per-rule screening changes without deployment.
- [x] Screening-event loss is durable or explicitly measured.
- [x] Sanctions screening runs offline from a local fixture and supports re-screening.
- [x] Documents are encrypted at rest and disabled cleanly by default.
- [x] Provider sandbox contract is deterministic and env-gated.
- [x] Metrics, dashboards, alerts, runbooks, and plan status are updated.
- [x] Full lint, test, vet, smoke, business, admin, and chaos gates pass.
