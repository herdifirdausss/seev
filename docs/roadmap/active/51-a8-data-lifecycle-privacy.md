# 51 — Track A8: Data Lifecycle and Privacy

> [Documentation home](../../README.md) · [Roadmap](../README.md) · [Active plans](README.md)

> Derived from track **A8** in
> [42-long-term-roadmap.md](../42-long-term-roadmap.md).
>
> **Status: ready for execution; not implemented.** The activation trigger is
> a conscious learning decision made on 2026-07-22. This is an engineering
> privacy baseline, not a claim of GDPR, Indonesian regulatory, or any other
> formal legal compliance.

## 1. Trigger and objective

The repository now stores enough real product data that “keep everything
forever” is no longer a safe default. Several tables contain short-lived or
sensitive fields even though their durable business record must remain:

- expired fee quotes, refresh tokens, admin sessions, and successful work
  queues have no cleanup path;
- pay-in webhook bodies, payout destinations, KYC payloads, reconciliation
  rows, and operator emails are stored in plaintext;
- KYC document bytes are envelope-encrypted, but object names include the user
  UUID and no deletion lifecycle exists;
- a user can read a profile but cannot request a complete data export;
- disabling an account does not pseudonymize its references in owner
  databases;
- deleting ledger idempotency keys naively would permit duplicate monetary
  posting, so privacy cleanup must preserve a non-reversible dedup tombstone;
- immutable ledger entries must remain untouched.

Track A8 introduces an explicit, owner-scoped lifecycle for data creation,
retention, export, redaction, pseudonymization, and deletion. The design must
reduce sensitive data while preserving money safety, auditability, recovery,
and service ownership.

### Measurable targets

These targets apply to the repository's local/staging fixture:

1. Every persisted table and object class has an owner, classification,
   retention rule, hold behavior, and purge/redaction action.
2. Eligible transient rows are removed or redacted within 24 hours of their
   policy cutoff when workers and dependencies are healthy.
3. No active, pending, held, unresolved, or legally/operationally held record
   is purged.
4. Sensitive fields selected by K2 have no plaintext copy after migration and
   verification.
5. A user export completes within ten minutes, is encrypted at rest, and is
   automatically destroyed after 24 hours or a successful one-time download.
6. An eligible account is pseudonymized across all owner databases within 15
   minutes without modifying `ledger_entries`.
7. Replaying a raw idempotency key after its privacy window still returns the
   original monetary result and never posts twice.
8. Every purge, export, hold, and pseudonymization transition is auditable
   without placing personal data in logs or metrics.

## 2. Live repository facts

The following facts were verified when this plan was written. Execution must
check the live code and use the next available migration numbers.

### 2.1 Identity and KYC

- `seev_auth.auth_users` stores email and full name in plaintext.
- Password hashes are separated in `auth_credentials`; refresh tokens are
  stored as SHA-256 hashes but are never deleted.
- `kyc_submissions.payload` is plaintext JSONB.
- `kyc_documents` stores metadata in PostgreSQL. Document bytes use AES-GCM
  envelope encryption when a document store and 32-byte KEK are configured.
- Current object keys contain the user UUID.
- KYC retry and level-change tables retain operational and audit history.

### 2.2 Money and product records

- `ledger_entries` is append-only with a database trigger that rejects update
  and delete.
- Ledger transaction headers hold raw idempotency key/scope values under a
  permanent unique index. Those values are needed for safe replay today.
- `fee_quotes` stores expired and consumed quotes indefinitely.
- Pay-in webhook rows retain a raw JSON body for replay/forensics.
- Payout requests retain a vendor-shaped destination JSON document.
- Reconciliation items may retain raw imported row data.
- Ledger outbox payloads remain after publication.

### 2.3 Operational and derived records

- Admin BFF sessions have idle and absolute expiration but no cleanup worker.
- Admin audit rows include operator email in plaintext.
- Gateway notifications, fraud screening events, assurance runs/findings,
  alert deliveries, intake commands, and completed payout vendor commands
  have no shared retention policy.
- Application roles generally lack `DELETE`, which is a useful safety
  boundary that must not be replaced with broad delete grants.

### 2.4 Existing privacy protections

- Logs mask passwords, tokens, authorization values, documents, raw webhook
  fields, payout destinations, and full idempotency keys.
- Service databases are isolated by roles and RLS.
- Internal service calls use mTLS identities and fail-closed credentials.
- Plan 50 defines backup retention and PITR but is not a selective erasure
  mechanism. Data removed from the active database may remain in encrypted
  backups until the backup chain expires.

## 3. Scope and anti-scope

### In scope

- a version-controlled retention and classification matrix;
- bounded owner-side purge and redaction procedures;
- retention holds that fail closed;
- cleanup of fee quotes, tokens, sessions, published outbox rows, successful
  work records, and expired export artifacts;
- encryption of sensitive auth, KYC, pay-in, payout, reconciliation, and admin
  fields;
- privacy-safe ledger idempotency tombstones;
- authenticated asynchronous user exports;
- account closure and cross-service pseudonymization;
- metrics, audit events, runbooks, and failure/restart tests;
- explicit interaction with A7 backup expiration.

### Out of scope

- formal legal certification or claims about statutory retention periods;
- deleting, updating, encrypting in place, or pseudonymizing
  `ledger_entries`;
- deleting financial transactions merely because a user closes an account;
- changing balances, lifecycle closers, fee evidence, or reconciliation
  outcomes;
- production KMS/HSM, external identity providers, or admin 2FA;
- analytics/CDC deletion propagation from C2, which does not exist yet;
- archival/partitioning from B2;
- a public “delete immediately” operation that bypasses pending-money and hold
  checks;
- storing plaintext export archives or privacy keys in Git, logs, or CI
  artifacts.

The retention periods below are conservative engineering defaults for this
learning repository. A real deployment must replace them with an approved
jurisdiction/product policy before handling real customer data.

## 4. Locked retention matrix

Retention is measured from the terminal or expiry timestamp, not merely row
creation. “Redact” means replacing a sensitive field with a fixed schema-safe
marker while retaining the business row. “Pseudonymize” means replacing a
user reference with the closure workflow's random surrogate UUID.

### 4.1 Permanent financial evidence

The following are never age-purged in this track:

| Owner | Data | Action |
| --- | --- | --- |
| Ledger | `ledger_entries` | Immutable; never update or delete |
| Ledger | posted transaction headers, lifecycle closers, accounts, balance snapshots | Retain; scrub only privacy fields explicitly listed below |
| Ledger | pending adjustments and executed reconciliation decisions | Retain financial decision; redact raw/import-only fields by policy |
| Pay-in | posted event and settled-intent correlation fields | Retain; redact raw body and pseudonymize user reference after closure |
| Payout | request amount, currency, vendor, state, hold/closer IDs, fee proof | Retain; redact destination/errors and pseudonymize user reference |
| Assurance | active/acknowledged findings and intake state | Retain while active |

### 4.2 Default transient and audit rules

| Owner/data | Eligibility | Default action |
| --- | --- | --- |
| Unconsumed fee quote | `expires_at` older than 24 hours | Delete |
| Consumed fee quote | consumed and booked proof older than 365 days | Delete after proof check |
| Raw ledger idempotency key/scope | terminal transaction older than 30 days | Null raw values; retain digest tombstone permanently |
| Published ledger outbox event | published more than 30 days | Delete payload row |
| Dead ledger outbox event | any age | Never automatic; operator resolves/replays first |
| Expired/revoked refresh token | terminal more than 30 days | Delete |
| Expired admin session | absolute expiry older than 7 days | Delete |
| Successful KYC apply retry | succeeded more than 90 days | Delete operational row |
| Dead KYC apply retry | dead more than 365 days | Delete after audit summary exists |
| KYC submission and document | account closed more than 365 days and no hold | Delete payload/object/metadata; retain pseudonymous level-change audit |
| Pay-in raw webhook body | event terminal more than 30 days | Redact raw body; retain allowlisted correlation columns |
| Payout destination and raw error | request terminal more than 30 days | Redact; retain monetary/lifecycle fields |
| Payout vendor call/command | terminal more than 365 days | Delete child operational rows; retain request summary |
| Reconciliation raw row/source filename | batch terminal more than 90 days | Redact raw/source value; retain match result and totals |
| Read notification | read more than 180 days | Delete |
| Any notification | older than 365 days | Delete |
| Fraud screening event | older than 365 days | Delete after aggregate audit metrics are recorded |
| Admin audit row | older than 365 days | Delete only when no hold applies |
| Assurance successful run | finished more than 90 days | Delete |
| Assurance failed run or failed alert delivery | terminal more than 180 days | Delete after incident/audit summary |
| Assurance resolved finding | resolved more than 365 days | Delete; active findings are never eligible |
| Applied/rejected intake command | terminal more than 365 days | Delete; pending/applying commands are never eligible |
| Privacy export artifact | successful download or age 24 hours | Cryptographic/object deletion plus metadata tombstone |
| Completed privacy workflow detail | completed more than 365 days | Delete sensitive detail; retain minimal audit tombstone |

Configuration, routing, policy, currency, sanctions, and current rule tables
are state rather than event history. Disabled/superseded rows require an
explicit owner policy and must not be deleted by a generic age rule.

### 4.3 Never-purge conditions

No retention job may purge a row when any of these applies:

- the row is pending, processing, retryable, held, open, acknowledged, or
  otherwise non-terminal;
- it participates in an unclosed monetary lifecycle;
- a retention hold covers its user, resource, table, or time range;
- its successor/audit summary has not been persisted;
- a required cross-service proof dependency is unavailable;
- the policy version is unknown or older than the row's recorded policy
  version;
- the job cannot prove that object-store deletion and metadata transition are
  consistent.

Ambiguity fails closed: skip and alert rather than delete.

## 5. Locked design decisions

### K1 — One machine-readable policy, enforced by each owner

Add `config/data-retention.yaml` as the version-controlled policy source. Each
entry defines owner, table/object class, classification, terminal timestamp,
duration, action, batch size, hold scope, and policy version. A generated
human-readable matrix in `docs/data/retention.md` must match it in CI.

Each service loads only its own section and rejects unknown actions, negative
durations, duplicate entries, and policies that target permanent financial
tables. Runtime overrides may shorten test durations but are forbidden in
production mode. A production change requires a reviewed policy-file change.

### K2 — Sensitive fields use versioned envelope encryption

Extract the existing KYC document envelope into a domain-neutral
`pkg/cryptox`. Use AES-GCM with a random data key, a versioned KEK ring, and
associated data containing service, table, column, row ID, and envelope
version. A ciphertext copied to another row or field must fail authentication.

Use a separate HMAC lookup key for deterministic equality lookups such as
normalized email. Encryption keys and lookup keys are separate, loaded from
Vault/environment, and never stored with ciphertext. The current key version
is used for writes; previous versions remain decrypt-only during rotation.

Encrypt these fields:

| Owner | Current field | Protected representation |
| --- | --- | --- |
| Auth | `auth_users.email` | ciphertext plus normalized-email HMAC digest |
| Auth | `auth_users.full_name` | ciphertext |
| Auth | `kyc_submissions.payload` | ciphertext |
| Auth/object store | KYC object key | opaque random path with no user UUID |
| Pay-in | `payin_webhook_events.raw` | ciphertext until redaction cutoff |
| Payout | `payout_requests.destination` | ciphertext until redaction cutoff |
| Ledger | `recon_items.raw`, source filename | ciphertext until redaction cutoff |
| Admin BFF | operator email in sessions/audit | session ciphertext; masked/digested audit identity |

Do not encrypt values required for indexed monetary verification, and do not
place ciphertext in logs, metrics, traces, or API errors.

### K3 — Encryption migration uses expand/backfill/contract

For each owner:

1. add nullable ciphertext, key-version, and lookup-digest columns;
2. write ciphertext for new rows while reading old plaintext as fallback;
3. backfill in bounded keyset batches with restartable progress;
4. compare counts, hashes, uniqueness, and decryptability;
5. make protected columns required and stop plaintext writes;
6. remove or null plaintext columns only after the verification gate.

Backfill progress is durable and contains row IDs/counts only. Post-contract
rollback is forward-fix or A7 restore; a down migration must not recreate
plaintext from logs or silently discard ciphertext.

### K4 — Delete capability remains constrained in PostgreSQL

Do not grant broad `DELETE` to normal application roles. Each owner migration
adds narrowly scoped `SECURITY DEFINER` retention functions that:

- set a safe `search_path` and fixed owner;
- derive eligibility from database state and policy version;
- accept only bounded batch size and a job UUID, not arbitrary SQL/cutoffs;
- use `FOR UPDATE SKIP LOCKED` or keyset batches;
- return affected IDs/counts without sensitive values;
- write an append-only retention audit row in the same transaction;
- refuse permanent tables and live states.

Application roles receive only `EXECUTE` on their owner's functions. CI must
prove they still cannot issue direct `DELETE` or unrestricted redaction.

### K5 — Retention holds are durable and local to every owner

Auth coordinates a retention hold, but every affected service persists a
local hold before it acknowledges the command. A hold has an idempotency UUID,
scope (`subject`, `resource`, `table`, or `time_range`), reason code, actor,
creation time, optional expiry, and status. Reasons are controlled codes; free
text is separately sanitized and never becomes a metric label.

Creating a hold requires `admin` or `admin_maker`. Releasing a hold requires a
different `admin` or `admin_checker`. If the hold source or local hold state is
unavailable, subject-scoped purge and pseudonymization fail closed.

### K6 — Retention workers are owner-scoped and restartable

Each service owns its scheduler, repository call, and metrics for its tables.
The default schedule is daily at 01:30 Asia/Jakarta with deterministic service
jitter. Runs reject overlap, limit each transaction to 500 rows, obey existing
statement/lock timeouts, and continue until the configured per-run cap is
reached.

Every action supports dry-run count mode. Object deletion uses an outbox:
first persist a delete intent, then delete the encrypted object idempotently,
then mark metadata redacted/deleted. A storage outage never causes metadata to
claim that an object was removed.

### K7 — Ledger idempotency becomes a privacy-safe tombstone

Raw ledger idempotency data cannot simply be deleted: accepting the same key
again could create a second monetary transaction. Add a keyed HMAC-SHA-256
digest over a canonical, length-delimited `(scope, key)` value. Store digest
and key version under a permanent unique constraint.

New posting and lookup paths compute the digest before database access. Raw
key/scope remain available for 30 days for compatibility and troubleshooting,
then are nulled by retention. The digest tombstone, transaction ID, status,
and conflict fingerprint remain indefinitely.

The idempotency key ring supports current and previous lookup versions.
Rotation backfills a new digest version before retiring the old key. A missing
key version fails posting closed; it never bypasses deduplication.

API and gRPC responses tolerate absent raw idempotency values after retention.
Logs continue to mask them before and after this change.

### K8 — Expired quote deletion is proof-aware

An unconsumed quote may be deleted 24 hours after expiry. A consumed quote is
deleted only after:

- `consumed_by_ref` points to the expected transaction or payout;
- booked fee amount/gateway proof matches;
- the consumer is terminal;
- the 365-day evidence window has passed;
- no hold applies.

Deletion is batched and concurrent-safe. Quote creation/consumption does not
share a long transaction with cleanup. A quote selected by a concurrent
consumer is locked or skipped, never deleted underneath consumption.

### K9 — User export is asynchronous, owner-composed, and encrypted

Auth owns `privacy_requests` and coordinates a versioned export. Public API:

```text
POST /api/v1/users/me/privacy/exports
GET  /api/v1/users/me/privacy/requests/{id}
GET  /api/v1/users/me/privacy/exports/{id}/download
```

Creating an export requires an authenticated user and password re-verification.
The request is idempotent and at most one active export is allowed per user.

Each owner exposes an additive internal privacy endpoint that accepts the
request UUID, subject UUID, cutoff, schema version, and bounded page cursor.
Only the verified `auth-service` mTLS identity plus internal credential may
call it. Responses contain allowlisted subject data, not secrets, password or
token hashes, internal credentials, raw sanctions datasets, other users'
data, or unrestricted audit/debug fields.

Auth assembles a versioned ZIP containing `manifest.json` and one NDJSON file
per owner. The manifest records schema versions, row counts, hashes,
generation cutoff, exclusions, and retention policy version. The archive is
encrypted at rest with a dedicated export KEK and an opaque object key.

Download rechecks JWT ownership and password, streams decrypted bytes without
writing plaintext to disk, then schedules one-time artifact deletion. An
undownloaded export expires after 24 hours. Metadata retains only a minimal
audit tombstone.

### K10 — Account closure is an idempotent cross-service saga

Public API:

```text
POST /api/v1/users/me/privacy/closure
GET  /api/v1/users/me/privacy/requests/{id}
```

Closure requires password re-verification and immediately disables new login,
revokes refresh tokens, and prevents new top-up/payout intake for that user.
Admin/operator accounts cannot use self-service closure; they require the
operator offboarding runbook and maker/checker approval.

Before pseudonymization, all owners must prepare successfully. Blocking
conditions include:

- any non-zero cash, hold, pending, frozen, or pocket balance;
- an open withdrawal lifecycle or non-terminal payout;
- a pending top-up, schedule, disbursement, adjustment, KYC retry, or privacy
  export;
- an active retention hold;
- an unresolved critical assurance finding for the subject's resources;
- an unavailable owner or failed integrity verifier.

Auth generates a random surrogate UUID and stores the original subject UUID
encrypted only while the saga is active. Owner commits replace mutable user
references with the surrogate, redact eligible sensitive fields, and return a
deterministic result hash/count. Operations are idempotent and retryable.

Auth finalizes last: remove credentials/tokens, replace direct identifiers
with fixed tombstone values, move retained KYC/audit references to the
surrogate, and destroy the active-saga ciphertext containing the original
UUID. The completed request keeps only request ID, surrogate, timestamps,
policy version, owner result hashes, and status.

`ledger_entries` and their account/transaction IDs remain byte-for-byte
unchanged. `accounts.owner_id` may change to the surrogate because ownership
is a mutable projection outside immutable entries. Ledger verification must
pass before and after the change.

### K11 — Pseudonymization ownership map

Each owner documents exactly which references are changed:

| Owner | References/actions |
| --- | --- |
| Auth | identity tombstone, credentials/tokens removal, KYC references and encrypted artifacts |
| Ledger | account owner, user policy/quote/schedule/disbursement references; no entry mutation |
| Pay-in | event/intent/routing user references; raw payload already redacted |
| Payout | request/routing user references; destination already redacted |
| Fraud | screening-event user references |
| Gateway | notification user references or deletion by policy |
| Admin BFF | operator session removal and audit identity pseudonymization when applicable |
| Assurance | verify evidence contains no hidden subject field; rewrite only explicitly classified evidence |

Owner APIs implement `prepare`, `commit`, and `status`. There is no rollback to
the original identity after commit starts. Failures resume forward from the
last durable owner state while the account remains disabled.

### K12 — Backup erasure is expiration-based

Active-database redaction does not rewrite retained A7 backups. Privacy status
and runbooks must state the latest backup-expiration date that may still
contain the old value. Backup access remains encrypted and restricted.

Once Plan 50 exists, lifecycle tests verify that new backups contain only the
post-redaction state and old chains expire according to their retention
policy. Before A7 is implemented, the repository must describe this as a
known limitation rather than claiming complete erasure from backups.

### K13 — Audit and observability contain no personal data

Use stable low-cardinality metrics:

```text
seev_retention_runs_total{owner,action,result}
seev_retention_rows_total{owner,class,action}
seev_retention_oldest_eligible_age_seconds{owner,class}
seev_retention_holds{owner,scope,status}
seev_privacy_requests{kind,status}
seev_privacy_request_duration_seconds{kind,result}
seev_privacy_owner_calls_total{owner,operation,result}
seev_privacy_object_delete_total{kind,result}
seev_pii_backfill_rows_total{owner,field,result}
```

Never use user IDs, emails, request IDs, object keys, table primary keys, or
free-text reasons as labels. Audit rows use actor ID, controlled action/result
codes, policy version, counts, and a request correlation ID. Logs must not
contain original/surrogate mappings.

## 6. Execution tasks

Execute T0 → T1 → T2 → T3 → T4 → T5 → T6. T1 quote cleanup can ship before
field encryption, but closure cannot ship until encryption, idempotency,
holds, and every owner contract are complete.

### T0 — Complete inventory and classification

**Work**

1. Re-enumerate every table, JSON field, object path, cache key, event payload,
   log field, and backup copy across all eight services.
2. Assign owner, classification (`public`, `internal`, `personal`,
   `sensitive`, `financial`, `secret`), retention action, hold scope, and
   export eligibility.
3. Create `config/data-retention.yaml`, its JSON schema, and generated
   `docs/data/retention.md`.
4. Add a CI test that fails when a migration creates a table not present in
   the matrix or marks a permanent ledger table purgeable.
5. Record data-size and eligible-row baselines for later batch testing.

**Required checks**

- all eight migration directories are covered;
- object store, Redis, RabbitMQ, logs, traces, and A7 backups are classified;
- policy schema rejects invalid and ambiguous rules;
- docs generation is deterministic;
- `git diff --check` passes.

**Definition of done:** no persisted class is ownerless or governed by an
implicit “keep forever” rule.

### Result

_Pending implementation._

### T1 — Holds, bounded retention engine, and transient cleanup (K1, K4–K6, K8)

**Work**

1. Add local hold and retention-audit tables to each owner database using the
   next migration numbers.
2. Add constrained database functions and owner workers with dry-run,
   bounded batches, overlap rejection, jitter, and restart-safe progress.
3. Implement fee-quote, refresh-token, session, published-outbox,
   notification, successful-retry, assurance-run, alert-delivery, and expired
   export cleanup.
4. Add object-delete outbox primitives before any KYC/export object cleanup.
5. Add internal admin endpoints/CLI for status, dry-run, run-now, hold create,
   and maker/checker hold release.
6. Add policy-lag and deletion-failure metrics and alerts.

**Required tests**

- eligibility boundary at exact cutoff and timezone transitions;
- 500-row batching, equal timestamps, concurrent workers, and restart;
- direct application `DELETE` remains forbidden;
- hold creation/release role separation and fail-closed behavior;
- active/pending/dead-unresolved rows are never removed;
- concurrent quote consumption beats or safely excludes cleanup;
- object outage preserves metadata and retries deletion;
- dry-run counts match actual affected rows.

**Definition of done:** safe transient data expires automatically without
broadening application database privileges or touching live money state.

### Result

_Pending implementation._

### T2 — Encrypt sensitive fields and remove plaintext (K2–K3)

**Work**

1. Extract and harden `pkg/cryptox` with versioned envelopes, AAD, KEK ring,
   deterministic lookup HMAC, zeroization where practical, and key metrics.
2. Add generated development keys, ignored Compose secrets, Vault seeding,
   production fail-fast validation, and key-rotation runbook.
3. Migrate auth email/full name/KYC payload and opaque KYC object paths.
4. Migrate pay-in raw webhook, payout destination, reconciliation raw/source,
   and admin session/audit identity fields.
5. Run bounded backfills, verification, contract migration, and plaintext
   absence scans.
6. Apply retention redaction to ciphertext fields without decrypting them in
   the cleanup worker.

**Required tests**

- envelope round-trip, wrong key, wrong AAD, copied ciphertext, truncated
  envelope, and old-key read/new-key write;
- normalized email lookup and uniqueness without plaintext;
- dual-read/write compatibility during backfill;
- restartable equal-timestamp keyset backfill;
- no plaintext sensitive value in database text/JSON columns, logs, traces,
  errors, metrics, or object paths;
- service boot fails when a required current key is missing;
- existing business, KYC, webhook replay, payout, and reconciliation behavior
  remains correct.

**Definition of done:** classified sensitive fields are encrypted or masked at
rest and every plaintext fallback has been removed after verification.

### Result

_Pending implementation._

### T3 — Idempotency digest tombstones (K7)

**Work**

1. Add digest/version/conflict-fingerprint columns and a unique digest index
   to ledger transactions.
2. Introduce canonical length-delimited digest input and versioned HMAC keys.
3. Backfill every existing transaction and prove there are no collisions or
   missing versions.
4. Switch post/lookup/replay paths to digest-first behavior while preserving
   temporary raw compatibility.
5. Add retention redaction of raw key/scope after 30 days.
6. Update protobuf/HTTP behavior and documentation for absent historical raw
   keys without exposing digest values.

**Required tests**

- same key/scope deduplicates before and after raw redaction;
- same key with a different scope remains distinct;
- conflicting amount/type returns the original idempotency conflict;
- concurrent retries have exactly one monetary effect;
- current/previous key versions work during rotation;
- missing/unknown key versions fail closed;
- digest/backfill migrations and proto checks pass;
- no digest or raw key appears in logs/metrics.

**Definition of done:** raw idempotency data is purgeable without weakening
the permanent monetary deduplication invariant.

### Result

_Pending implementation._

### T4 — Authenticated user export (K9)

**Work**

1. Add auth privacy-request/export migrations, repository, worker, public
   routes, password re-verification, ownership, and rate limits.
2. Add route-level internal service authentication and paginated owner export
   contracts for auth, ledger, pay-in, payout, fraud, gateway, admin BFF, and
   assurance classification.
3. Add deterministic versioned export DTOs with explicit included/excluded
   fields and stable ordering.
4. Build encrypted ZIP/NDJSON artifacts with manifest hashes and owner counts.
5. Add one-time streaming download, 24-hour expiry, object-delete outbox, and
   audit tombstone.
6. Add `scripts/privacy-export.sh` for local/operator testing without printing
   archive contents.

**Required tests**

- cross-user IDOR attempts, missing password, disabled user, and role checks;
- owner timeout/retry, pagination, duplicate request, and partial assembly;
- export contains the subject's expected data and no other user's data;
- password/token hashes, internal secrets, raw sanctions data, and unclassified
  fields are absent;
- artifact is encrypted at rest, plaintext is never written to disk, and wrong
  KEK fails;
- successful download and TTL expiry each remove the object idempotently;
- a failed owner never produces a falsely complete manifest.

**Definition of done:** a user can retrieve a complete, bounded, encrypted,
owner-sourced export without direct cross-database reads by the public API.

### Result

_Pending implementation._

### T5 — Account closure and pseudonymization saga (K10–K12)

**Work**

1. Extend privacy requests with closure state, encrypted active subject,
   surrogate UUID, owner checkpoints, result hashes, retry/backoff, and dead
   status.
2. Implement password-confirmed self-service closure and separate
   maker/checker operator offboarding.
3. Add owner `prepare`, `commit`, and `status` contracts and idempotent local
   transactions for every mapping in K11.
4. Enforce zero-balance, no-open-work, no-hold, assurance, and dependency
   preconditions.
5. Disable access first, commit owners, finalize auth last, destroy the active
   original-ID ciphertext, and keep a minimal audit tombstone.
6. Run ledger and product assurance verification before and after commit.
7. Report active-database completion and the A7 backup-expiration horizon
   separately.

**Required tests**

- every blocking condition and an eligible happy path;
- crash/restart before prepare, between owners, before auth finalization, and
  after finalization;
- duplicate commands do not change result counts or create a second surrogate;
- one unavailable owner leaves the user disabled and resumes forward later;
- hold appears during prepare and prevents commit;
- old login, refresh token, admin session, user routes, and old subject lookup
  fail after completion;
- all owner references use the surrogate or are deleted by policy;
- `ledger_entries` checksums are byte-for-byte identical before/after;
- balances, lifecycle, ledger verifier, and assurance remain clean;
- logs/audit never expose original-to-surrogate mapping.

**Definition of done:** an eligible user can be de-identified across service
boundaries while financial evidence and monetary integrity remain intact.

### Result

_Pending implementation._

### T6 — Operations, chaos, backup interaction, and final gate (K12–K13)

**Work**

1. Add lifecycle/privacy dashboards, alerts, runbooks, and admin BFF status
   panels without exposing subject data.
2. Add `scripts/privacy-e2e.sh` covering export, retention, hold, and closure.
3. Add focused failure drills for database outage, object-store outage, key
   mismatch, worker kill/restart, owner timeout, and retention/closure races.
4. If Plan 50 is implemented, prove new backups exclude redacted plaintext and
   old chains expire on schedule. Otherwise record the backup limitation in
   API/docs and do not claim backup erasure.
5. Run plaintext scans on PostgreSQL dumps, object names, logs, Tempo/Loki
   fixtures, and sanitized CI diagnostics.
6. Record row counts, purge duration, export duration/size, closure duration,
   retries, and final integrity evidence.
7. Mark A8 complete only after every acceptance item has evidence.

**Required final gate**

```bash
GOCACHE=/tmp/seev-go-cache go build ./...
GOCACHE=/tmp/seev-go-cache go vet ./...
GOCACHE=/tmp/seev-go-cache go vet -tags=integration ./...
GOCACHE=/tmp/seev-go-cache make test
GOCACHE=/tmp/seev-go-cache make lint
make proto
make proto-lint
make proto-breaking
GOCACHE=/tmp/seev-go-cache go test -tags=integration -race ./...
GOCACHE=/tmp/seev-go-cache ./scripts/smoke-test.sh all
GOCACHE=/tmp/seev-go-cache ./scripts/business-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/admin-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/privacy-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/chaos-test.sh all
git diff --check
```

**Definition of done:** lifecycle and privacy behavior is measurable,
restart-safe, operator-usable, and verified without weakening money safety or
claiming legal certification.

### Result

_Pending implementation._

## 7. Acceptance checklist

### Inventory and retention

- [ ] Every database table, object class, event payload, and cache class is in
      the versioned policy matrix.
- [ ] CI rejects unclassified new tables and purge rules targeting immutable
      ledger data.
- [ ] Owner workers purge/redact all eligible default classes in bounded
      batches.
- [ ] Holds, live states, unknown policy versions, and unavailable proof fail
      closed.
- [ ] Normal application roles still cannot execute direct unrestricted
      deletion.

### Sensitive-data protection

- [ ] Auth/KYC, pay-in raw data, payout destination, reconciliation raw data,
      and operator identity fields have no plaintext database copy.
- [ ] KEK and lookup-key separation, versioning, rotation, and wrong-key
      behavior are tested.
- [ ] Object keys, logs, metrics, traces, errors, and diagnostics contain no
      prohibited personal data.
- [ ] KYC and export object deletion is idempotent and metadata never lies
      about storage state.

### Idempotency and money safety

- [ ] Historical raw idempotency keys are redacted after 30 days.
- [ ] Permanent digest tombstones preserve replay and conflict behavior.
- [ ] Concurrent replay after redaction has exactly one monetary effect.
- [ ] Fee-quote cleanup cannot race consumption or delete unverified fee proof.
- [ ] Ledger entries are byte-identical across pseudonymization.

### Export and closure

- [ ] Export ownership, re-authentication, pagination, encryption, one-time
      download, expiry, and exclusions pass.
- [ ] Closure refuses every open-money, pending-work, hold, and dependency
      condition.
- [ ] Eligible closure converges after injected owner failures and restart.
- [ ] All classified references are pseudonymized or purged with no mapping in
      logs/audit.
- [ ] Ledger and assurance verification pass before and after closure.
- [ ] Backup-retention limitations and expiration horizon are explicit.

### Operations

- [ ] Metrics and dashboards use only bounded labels.
- [ ] Runbooks cover hold, failed purge, failed export, stuck closure, key
      rotation, object deletion, and backup residuals.
- [ ] Privacy E2E and focused failure drills pass twice consecutively.
- [ ] Full build, vet, lint, race, integration, smoke, business, admin, chaos,
      proto, and diff gates are green.

## 8. Global Definition of Done

- [ ] T0–T6 results contain commands, concise evidence, timings, and commit
      IDs.
- [ ] No immutable financial evidence is removed or altered.
- [ ] No sensitive plaintext or original/surrogate mapping appears in source,
      runtime output, or test artifacts.
- [ ] Policy defaults and legal/non-legal boundaries are documented clearly.
- [ ] The plan index and roadmap mark A8 complete only after all evidence is
      recorded here.

## 9. Explicit follow-ups

The following remain outside A8:

1. jurisdiction-specific retention approval and legal certification;
2. production KMS/HSM and external key escrow;
3. deletion propagation to a future CDC/warehouse platform;
4. off-site backup deletion beyond A7 retention expiry;
5. partitioning/archival and production-scale purge performance from B0/B2;
6. privacy handling for future B2B tenants and API keys from C1.
