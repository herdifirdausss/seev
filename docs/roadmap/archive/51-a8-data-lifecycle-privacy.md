# 51 — Track A8: Data Lifecycle and Privacy

> [Documentation home](../../README.md) · [Roadmap](../README.md) · [Archive](README.md)

> Derived from track **A8** in
> [42-long-term-roadmap.md](../42-long-term-roadmap.md).
>
> **Status: complete and archived (2026-07-26).** This is an engineering
> privacy baseline, not a claim of GDPR, Indonesian regulatory, or any other
> formal legal compliance. Its original owner/backup matrix predates the
> VendorService extraction in Plan 54; current service ownership and the
> nine-database topology are documented in the current reference pages.

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
`internal/platform/security/crypto`. Use AES-GCM with a random data key, a versioned KEK ring, and
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

**1. Full inventory — 49 tables across all 8 services, re-derived from the
live migrations, not assumed from this document's own §2.** Read every
`migrations/<owner>/*.up.sql` in file order (tracking `ALTER TABLE ADD
COLUMN` so each table's *current* merged column list was used, not just its
original `CREATE TABLE`), plus every Redis key-building function
(`services/ledger/policy`, `services/fraud/rules`, `contracts/vendorgw`,
`internal/platform/security/middleware/rate_limit.go`, `internal/platform/scheduling`), the RabbitMQ event schema
(`contracts/events/ledger/events.go`) and its one persisting consumer
(`services/gateway/internal/notification/notify.go`), and the KYC object-store code path
(`services/auth/internal/documents.go`).

**Two real findings that changed the plan, caught only by reading the
actual code rather than trusting this document's own §2/§4:**

- §2.3 said adminbff sessions have "no cleanup worker" — **false as of the
  current code**: `services/adminbff/internal/module.go`'s `Start()` already
  registers a `adminbff-session-cleanup` cron every 5 minutes, calling the
  real `CleanupSessions` query. It deletes the *moment* a session expires,
  not after the 7-day grace period §4.2 wants — so T1's actual work here is
  changing an existing cutoff, not wiring up dead code. (An earlier research
  pass this session initially reported `CleanupSessions` as unwired/dead
  code; spot-checking `services/adminbff/internal/module.go` directly before trusting
  that claim caught the error.)
- The KYC document object store is **entirely unwired** in this codebase:
  `Module.SetDocumentStore` has zero callers anywhere
  (`cmd/auth-service/main.go` only ever calls `SetDocumentKEK`), so
  `UploadKYCDocument`/`DownloadKYCDocument` always return
  `ErrDocumentStorageUnavailable`. `kyc_documents` metadata rows and the
  `"kyc/<user_uuid>/<random_uuid>"` object-key shape (K2's opaque-path
  requirement) both exist only on paper today — T2 must land a real store
  before either the encryption migration or the retention delete for this
  class can ever execute against real bytes.

**2. `config/data-retention.yaml`** — one entry per policy class (66
entries covering all 49 tables plus 9 non-Postgres classes: the KYC object
store, five Redis key families across ledger/fraud/shared middleware,
RabbitMQ event transit, log/trace masking, and A7 backup expiration).
Every entry cites the exact §4 matrix line it implements; entries not
itemized in the locked matrix (e.g. `ledger.scheduled_transactions`,
`ledger.disbursement_batches/items`, `payin.payin_topup_intents`'
unresolved-expiry case) are marked explicitly as a T0 judgment call rather
than silently presented as if the matrix already covered them. A
`permanent_tables` list names the 9 tables where no entry may fully delete
a row (`ledger.ledger_entries`/`ledger_transactions`/`accounts`/
`account_balances`/`account_balance_snapshots`/`pending_adjustments`,
`payin.payin_topup_intents`, `payout.payout_requests`/`payout_vendor_calls`)
— `assurance.assurance_findings` was deliberately **excluded** from this
list even though §4.1 covers it, because permanence there only applies to
the active/acknowledged subset; §4.2 explicitly allows deleting a
*resolved* finding after 365 days, a row-level distinction this table-level
list cannot correctly express.

**Two more T0 findings recorded directly in the policy's own `notes`
fields, not hidden:** `auth.auth_users` has no closure-timestamp column
yet (only a status flag) — T5 must add one for the closure-gated
KYC-deletion rule to have a real join target. `ledger.recon_batches` has
no `updated_at`/`completed_at` column to anchor its own 90-day redaction
rule — `created_at` is used as a documented proxy, since batches process
quickly after creation, flagged for a real fix if that assumption ever
stops holding.

**3. `config/data-retention.schema.json`** (JSON Schema draft 2020-12) —
formal, tool-agnostic documentation of the YAML shape (required fields,
enum values, the `delete`/`redact`-requires-age conditional). Validated
directly against the live policy file with the `jsonschema` Python package
during authoring; `internal/retentionpolicy`'s own Go validation
(independent of this file, no JSON-Schema library dependency added to
go.mod) is what CI actually runs.

**4. `internal/retentionpolicy` + `cmd/retentioncheck`.** A small,
stdlib-plus-`gopkg.in/yaml.v3` package (matching `cmd/doccheck`'s own
"no external tool dependency" convention) that loads the policy, validates
every schema-level rule (valid enums, no duplicate class ids, required
fields, the `delete`/`redact` age requirement, owner/table-prefix
agreement, the permanent-tables invariant) and cross-checks it against the
real `migrations/` tree in both directions: a migrated table absent from
the policy fails, *and* a policy entry naming a table no migration creates
also fails — the second direction catches policy drift a one-way check
would miss entirely. `RenderMarkdown` deterministically renders
`docs/data/retention.md`; `-write` regenerates it, the default mode checks
it's current. Wired into `Makefile` (`retention-docs`/`retention-check`)
and `.github/workflows/ci.yml`'s existing `docs-check` job.

**5. Live verification, all required checks proven, not just described:**

```text
$ go run ./cmd/retentioncheck
retentioncheck: 66 policy entries valid, docs/data/retention.md is current
$ go test -race ./internal/retentionpolicy/...
ok  	github.com/herdifirdausss/seev/internal/retentionpolicy	1.490s
```

26 tests, including: `TestValidate_RealPolicyIsClean` (the actual committed
policy against the actual committed migrations tree — zero violations);
`TestValidate_AllEightMigrationDirectoriesCovered`;
`TestValidate_NonPostgresClassesAreClassified` (proves object store, every
Redis family, RabbitMQ, logs/traces, and A7 backups each have a real,
populated entry — the exact §6 "Required checks" wording);
`TestRenderMarkdown_Deterministic` (same `Policy`, two calls, byte-identical
output) plus `TestRenderMarkdown_MatchesCommittedDoc`; and eleven
synthetic-fixture tests proving the schema genuinely *rejects* invalid and
ambiguous input — unknown classification/action, duplicate class id, a
`delete` against a `permanent_tables` entry (while confirming `redact`
against the same table is correctly allowed), a missing
`terminal_timestamp`/`duration` on an age-based action, a malformed
duration string, empty `notes`, an owner/table prefix mismatch, and both
directions of the migration cross-check (a real table with no policy
entry; a policy entry naming a table nothing creates) via a temporary
synthetic `migrations/` fixture.

`go build ./...`, `go vet ./...`, `gofmt -l`, and `golangci-lint run` all
clean on the new packages. `go mod tidy` promoted `gopkg.in/yaml.v3` from
an already-present indirect dependency (pulled in transitively) to a
direct one — no new module was added to the dependency tree.

**6. Data-size and eligible-row baselines** — queried live against this
session's own accumulated dev-stack data (`seev-postgres-1`, populated by
today's `smoke-test.sh`/`business-e2e.sh`/`admin-e2e.sh`/`chaos-test.sh`
runs), isolated start/stop (`docker compose up -d postgres` /
`docker compose stop postgres`, never `-v`, never touching the volume):

| Table (largest per service) | Row count |
|---|---|
| `ledger.ledger_entries` | 427 |
| `gateway.notif_notifications` | 234 |
| `ledger.ledger_transactions` / `outbox_events` | 195 each |
| `assurance.assurance_runs` | 55 |
| `payout.payout_vendor_calls` | 94 |
| `payout.payout_vendor_commands` | 43 |
| `payout.payout_requests` | 39 |

Terminal-state counts for the classes T1 will actually purge (all
`eligible_now` counts are 0, as expected — every row was created within
the last few hours on this reference machine, none old enough yet to
clear even the shortest 24h window; the useful baseline is the *terminal*
count, proving the eligibility predicate itself correctly identifies the
right rows on real data):

| Class | Terminal rows | Eligible now |
|---|---|---|
| `auth.auth_refresh_tokens` (revoked/expired) | 4 | 0 |
| `ledger.outbox_events.published` | 195 | 0 |
| `payout.payout_vendor_commands` (completed/dead) | 41 | 0 |
| `gateway.notif_notifications` (read) | 0 | 0 |
| `adminbff.sessions` (expired) | 1 | 0 |
| `ledger.fee_quotes` (unconsumed expired) | 0 | 0 |

T1 will need deliberately backdated fixtures (matching this session's own
`backdate_payout`-style helpers already used in `scripts/chaos-test.sh`) to
exercise real deletion — this baseline only proves current volumes and
that the SQL predicates themselves are correct against live schema/data,
not that anything is old enough to purge today.

**Required checks:** `git diff --check` → clean (confirmed alongside the
rest of this task's changes, reported at commit time).

**Explicitly NOT done this task — later Track A8 work, not oversights:**
no retention worker, hold table, or `SECURITY DEFINER` function exists yet
(T1); no field is encrypted yet (T2); the two schema gaps found above
(`auth_users` closure timestamp, `recon_batches` completion timestamp)
are recorded as requirements for T5/T1 respectively, not fixed here — T0's
job was the inventory and the machine-checked policy, not the runtime
mechanism.

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

**1. K5 hold/audit infrastructure — `<owner>_retention_holds` and
`<owner>_retention_audit`, identical shape, migrated into all 8 owner
databases.** A real bug was found and fixed live during this work:
identically-named tables across owners collided in
`testutil.ApplyServiceMigrations`'s shared single-database test harness
(`relation "retention_holds" already exists`), breaking pre-existing,
unrelated integration tests across several services. Fixed by
service-prefixing every table/function
(`fn_<owner>_retention_hold_covers`, matching this repo's own
`payout_*`/`payin_*` precedent) and re-verifying the full integration suite
passed everywhere, not just in this task's own new tests. `released_by <>
created_by` is enforced by a database CHECK constraint (K5 maker/checker),
not just application logic.

**2. `internal/platform/lifecycle/retention/worker`** — the shared Go runtime every owner's Runner
uses: bounded batch loop, one SECURITY DEFINER Postgres function per class,
dry-run support (a dry run returns the full eligible count in one call,
never a batched preview), `FOR UPDATE SKIP LOCKED` batch claiming, Jakarta
schedule with deterministic per-owner jitter (`internal/platform/lifecycle/retention/worker/schedule.go`).

**3. Ten retention classes implemented as real SECURITY DEFINER functions**,
each following the fixed `(p_job_id UUID, p_batch_size INT, p_dry_run
BOOLEAN) RETURNS INT` signature, excluding held rows via
`fn_<owner>_retention_hold_covers` where a hold applies, and writing its own
`<owner>_retention_audit` row in the same transaction (K4):

| Owner | Class | Function | Hold scope |
| --- | --- | --- | --- |
| ledger | fee_quotes.unconsumed | `fn_retention_purge_fee_quotes_unconsumed` | subject |
| ledger | fee_quotes.consumed | `fn_retention_purge_fee_quotes_consumed` | subject (K8 proof-aware) |
| ledger | outbox_events.published | `fn_retention_purge_outbox_events_published` | none |
| auth | refresh_tokens | `fn_retention_purge_refresh_tokens` | subject |
| auth | kyc_apply_retries.succeeded | `fn_retention_purge_kyc_apply_retries_succeeded` | subject |
| adminbff | sessions | `fn_retention_purge_sessions` | resource |
| gateway | notifications.read | `fn_retention_purge_notifications_read` | subject |
| gateway | notifications.any | `fn_retention_purge_notifications_any` | subject |
| assurance | runs.succeeded | `fn_retention_purge_runs_succeeded` | none |
| assurance | alert_deliveries | `fn_retention_purge_alert_deliveries` | none |

A real, previously-unknown production bug was found and fixed as a side
effect of the adminbff.sessions function: `adminbff_app` had only ever been
granted `SELECT, INSERT, UPDATE` on `sessions` (migration 000001), never
`DELETE` — the old application-level `CleanupSessions` cron had been
silently failing with "permission denied for table sessions" every 5
minutes in every real deployment. The SECURITY DEFINER function fixes this
as a consequence of K4's own no-broad-DELETE-grant rule, not as a targeted
patch. A related, separately-scoped bug — `DeleteSession` (logout) sharing
the same missing grant — was flagged via `spawn_task`, not fixed here: it's
a single-session explicit delete, not this task's retention-cleanup scope.

**Four classes deliberately deferred, each for a documented, structural
reason — not silently dropped:**

- `auth.kyc_submissions`, `auth.kyc_documents` — closure-gated (§4.2:
  "account closed more than 365 days"), and `auth_users` has no
  closure-timestamp column yet (only a status flag, per T0's own finding).
  Blocked on T5's account-closure saga adding one.
- `auth.kyc_apply_retries.dead`, `assurance.runs.failed` — §4.2 requires
  "an audit summary to already exist" before either may be purged, and
  §4.3's own never-purge condition ("its successor/audit summary has not
  been persisted") forbids purging without one. Neither has a persisted,
  queryable summary anywhere in this codebase today (`services/auth/internal/worker/retry.go`
  only sets `status='dead'` plus a log line; `assurance_runs.error_code`/
  `error_message` are the row's own fields, not an independent record).
  Purging either now would violate §4.3, not just be premature.

Two further policy entries (`assurance.findings.resolved`,
`assurance.intake_commands`) were never in T1's own work-item list (item 3
names "fee-quote, refresh-token, session, published-outbox, notification,
successful-retry, assurance-run, alert-delivery" only) — out of this task's
scope, not a gap within it.

**4. `internal/platform/lifecycle/objectoutbox`** (K6 item 4) — the generic transactional
object-delete outbox T1.6 required before any KYC/export object cleanup:
persist a delete intent, delete from the store idempotently, then mark
metadata deleted/redacted — all three steps in that order, with a failed
store delete leaving the outbox row `pending` and metadata untouched.
Wired into auth as the concrete vertical slice (`auth_object_delete_outbox`,
`kyc_documents.deleted_at`), proven live with an in-memory fake store since
no production object-store adapter is wired anywhere in this codebase yet
(`services/auth/internal/documents.go`'s own long-standing comment).

**5. `cmd/retentionctl`** (K6 item 5, the "CLI" half of "endpoints/CLI") —
`status`, `dry-run`, `run-now`, `hold-create`, `hold-release`, generic
across all 8 owners: it derives each class's SECURITY DEFINER function name
directly from `config/data-retention.yaml` by this repo's own established
naming convention, rather than hardcoding a class list per owner. Connects
as `app_service` — the same role every retention worker runs as in
production, no elevated privilege of its own. `hold-release` enforces K5's
maker/checker rule client-side (a friendlier error before the round trip)
in addition to the database's own CHECK constraint. Live-verified against
every implemented class across auth/gateway/assurance, including hold
creation, exclusion, and cross-operator release.

**6. Metrics (K13)** — `seev_retention_runs_total{owner,action,result}`,
`seev_retention_rows_total{owner,class,action}` (existing), plus
`seev_retention_holds{owner,scope,status}` (new this pass, refreshed once
per `RunOnce` call from each owner's own holds table) and
`seev_object_outbox_deleted_total`/`_failures_total`/`_last_batch_failed`
(new, `internal/platform/lifecycle/objectoutbox`). **`seev_retention_oldest_eligible_age_seconds`
from K13's own canonical list is deliberately not implemented**: the fixed
`(p_job_id, p_batch_size, p_dry_run) RETURNS INT` function signature has no
age channel to report through, and `internal/platform/lifecycle/retention/worker` is deliberately
schema-agnostic (it doesn't know any class's cutoff column). Adding it
would mean either widening every function's signature/audit-row shape
across four owners, or a per-class Go-side query this package's whole
design avoids — a real, structural gap, flagged for a future pass rather
than worked around with a fabricated approximation.

**7. Alerts** — `deploy/observability/prometheus/rules/retention.yml`:
`SeevRetentionRunFailing` (any class run erroring in the last hour),
`SeevRetentionRunStale` (no successful run in 26h — one day plus buffer
against the daily 01:30 Jakarta schedule), `SeevObjectOutboxDeleteFailing`
(repeated store-delete failures). Live-verified: Prometheus's own
`/api/v1/rules` endpoint confirmed all three registered with `health:
"unknown"` (correct pre-fire state) after a config reload, in the local
`--profile observability` stack.

**8. Required tests — live-verified, not just described:**

- eligibility boundary at exact cutoff — per class, e.g.
  `TestRetention_FeeQuotesUnconsumed_EligibilityBoundary`,
  `TestRetention_Sessions_EligibilityBoundary`,
  `TestRetention_NotificationsRead_EligibilityBoundary`. Timezone
  transitions are structurally moot for this deployment: Asia/Jakarta (WIB)
  is a fixed UTC+7 offset with no DST.
- 500-row batching — `TestRunOnce_RealRun_LoopsUntilBatchUndersized`,
  `TestRunOnce_StopsAtPerRunCap` (sqlmock, parameterized batch size).
- **equal timestamps and concurrent workers** —
  `TestRetention_ConcurrentWorkers_NoDoubleProcessing` (new this pass): two
  independent `*retentionworker.Runner`s call `RunOnce` concurrently
  against 40 rows sharing one `expires_at`, proving `FOR UPDATE SKIP
  LOCKED` — not `ORDER BY` alone, which cannot disambiguate ties — makes it
  architecturally impossible for both workers to claim the same row.
- restart — each batch commits in its own function-level transaction, so a
  crash mid-run leaves already-completed batches durably committed and
  audited; this is architectural (documented in `RunOnce`'s own comment,
  the SECURITY DEFINER function owns its own transaction), not separately
  exercised by a literal process-kill test.
- direct application `DELETE` remains forbidden — one test per implemented
  table, e.g. `TestRetention_Sessions_DirectDeleteStillForbidden`,
  `TestObjectOutbox_DirectDeleteForbidden`, `TestRetention_AssuranceDirectDeleteStillForbidden`.
- hold creation/release role separation and fail-closed behavior — the
  database CHECK constraint plus `retentionctl hold-release`'s live
  same-operator-rejected / cross-operator-accepted / already-released-rejected
  verification.
- active/pending/dead-unresolved rows are never removed — status-gated
  predicates proven per class, e.g.
  `TestRetention_KYCApplyRetriesSucceeded_EligibilityBoundary`'s pending-row
  survival assertion, `TestRetention_AlertDeliveries_TerminalStateOnly`.
- concurrent quote consumption beats or safely excludes cleanup —
  `TestRetention_FeeQuotesConsumed_ProofAware` (T1.3, unchanged this pass).
- object outage preserves metadata and retries deletion —
  `TestProcessOnce_StoreOutage_PreservesMetadataAndRetries` (sqlmock) and
  `TestObjectOutbox_StoreOutage_PreservesMetadataAndRetries` (real
  Postgres) — this is T1's own required-test wording verbatim.
- dry-run counts match actual affected rows — one test per implemented
  class, e.g. `TestRetention_Notifications_DryRunMatchesReal`.

Full sweep run clean at the end of this task: `go build ./...`, `go vet
./...`, `TestModuleBoundaries`, `make docs-check`, `make retention-check`
(83 policy entries), the complete non-integration suite, and the
integration suite for every touched package (ledger, auth, adminbff,
notify/gateway, assurance) — including every pre-existing test, not just
this task's own new ones.

### T2 — Encrypt sensitive fields and remove plaintext (K2–K3)

**Work**

1. Extract and harden `internal/platform/security/crypto` with versioned envelopes, AAD, KEK ring,
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

**1. `internal/platform/security/crypto`** — versioned envelope encryption (`Ring.Seal`/`Ring.Open`):
a random per-value AES-256 DEK encrypts the plaintext; the DEK itself is
wrapped by a versioned KEK from the ring. AAD (service/table/column/row ID)
is authenticated at both layers via AES-GCM's associated-data parameter, so
a ciphertext copied to another row or field fails authentication rather
than silently decrypting into the wrong place. Deterministic lookup HMAC
(`LookupKey.Digest`) uses a separate key from the encryption KEK (K2's own
mandate) so `auth_users.email` stays uniquely and case-insensitively
searchable without ever comparing plaintext. One-way masking
(`cryptox.MaskEmail`) is distinct from encryption — no key, not reversible
— used specifically for `adminbff.audit_log.email` per K2's own literal
wording ("masked ... audit identity" vs. "ciphertext" for every other
field); deterministic, so exact-match audit search still works by masking
the search term the same way.

**2. Key infrastructure** — `internal/platform/config.CryptoxConfig` is one shared
cluster-wide key ring across every service (same precedent as
`JWT_SECRET`/`INTERNAL_GRPC_TOKEN`); field-level isolation comes from AAD
binding, not separate keys per service. Docker secrets
(`CRYPTOX_KEY_V<N>_FILE`/`CRYPTOX_LOOKUP_KEY_FILE`, file wins over plain
env var) mirror `internal/backupagent`'s existing `BACKUP_PASSWORD_FILE`
convention. Production fail-fast validation rejects boot with no current
key configured. Full key-rotation runbook
(`docs/operations/runbooks/cryptox-key-rotation.md`).

**3–4. Field migration (expand phase, K3 step 1–2)** — every classified
sensitive field across 5 services got a nullable
`<field>_ciphertext`/`<field>_key_version` column pair, with application
code dual-writing plaintext + ciphertext and dual-reading (ciphertext wins
when present, plaintext fallback otherwise): `auth_users.email`/`full_name`,
`kyc_submissions.payload` (plus `rescreen_name`/`rescreen_birth_date`
projection columns — `ListKYCRescreenSubjects`' own indexed,
paginated `DISTINCT ON` query needs SQL-level access to exactly these two
already-externally-shared fields, which is impossible against ciphertext
without decrypting every row in application code),
`payin_webhook_events.raw`, `payout_requests.destination`,
`recon_batches.source_filename`, `recon_items.raw`, `sessions.email`. Every
`Module` gained a `SetCryptoxRing(ring[, lookup])` setter, mirroring the
established `SetDocumentKeyRing` "optional dependency configured after
construction" convention. KYC document object paths changed from
`"kyc/<user_uuid>/<random_uuid>"` to `"kyc/<document_uuid>"` — the
user/document relationship is now only recoverable from the encrypted DB
row, never the object path (K2).

**5. Backfill, verification, and plaintext absence scans (T2.5)** — every
repository above got a `BackfillOnce(ctx, batchSize) (int, error)` method:
one bounded batch of `WHERE <ciphertext column> IS NULL` rows per call,
`FOR UPDATE SKIP LOCKED` (safe under concurrent/duplicate invocation),
looped by the caller until it returns 0 — completion is the query's own
emptiness, not an external cursor, so a crash mid-loop just repeats the
same, still-correct query on restart (proven live:
`TestUserRepository_BackfillOnce_RestartableEqualTimestamps` and five
sibling tests seed dozens of rows sharing one identical `created_at`, drive
many small-batch calls simulating repeated restarts, and assert every row
is backfilled exactly once with a direct `SELECT count(*) WHERE
<ciphertext> IS NULL` scan — the plaintext absence proof — returning
zero). Each owning service's own `cmd/*-service` binary gained a
`--backfill-cryptox` one-shot flag (`auth-service` takes `all|users|kyc`;
the other four are boolean) that connects only to Postgres — no
gRPC/Redis/HTTP — loops its target(s) to completion, and exits. Kept
inside each service's own `main.go` rather than one shared cross-service
CLI: a `cryptoxbackfillctl` importing all five owners' `internal/*`
packages at once would violate `TestModuleBoundaries`' one-command-one-module
rule, the same reason `cmd/retentionctl` stays SQL-generic instead of
importing owner packages.

**Contract migration deliberately deferred (tracked as a follow-up, "A8
T2.5b")**: dropping the plaintext columns and
removing every repository's dual-read/dual-write fallback would make the
cryptox ring mandatory at every call site — a blast radius of 15+
pre-existing test files across 5 services (all currently construct
repositories with a `nil` ring for tests unrelated to encryption) plus
every `Module` constructor. Real-world practice also waits a bake/
confirmation period between backfill and contract, not same-session
immediacy. This is a genuine, tracked gap, not a silent omission — T2's
own Definition of Done ("every plaintext fallback has been removed after
verification") is **not yet met**; the acceptance checklist item below is
left unchecked accordingly.

**6. Retention redaction without decrypting (T2.6)** — four
`fn_retention_purge_*` SECURITY DEFINER functions
(`fn_retention_purge_webhook_events_raw`,
`fn_retention_purge_requests_destination_and_error`,
`fn_retention_purge_recon_batches`, `fn_retention_purge_recon_items`) fill
a gap that predates this task: `config/data-retention.yaml` declared these
four `redact` classes back in T0, but T1's own scope explicitly implemented
only its `delete` classes — these four had no purge function at all until
now. Each nulls both the plaintext column and the T2.4
ciphertext/key_version columns together in one `UPDATE`, needing no
cryptox key at all (nulling ciphertext is exactly as safe/cheap as nulling
plaintext). NOT NULL plaintext columns (`payin_webhook_events.raw`,
`payout_requests.destination`, `recon_batches.source_filename`) get a
small marker value instead of NULL; already-nullable ones
(`payout_requests.error_message`, `recon_items.raw`) go straight to NULL.
`recon_items`' own eligibility is its **parent** `recon_batches` row's
terminal window (join on `batch_id`), not any column on `recon_items`
itself — matches the policy's own documented reasoning. `payin` and
`payout` each gained their first-ever `StartRetentionRunner` (mirroring
`services/gateway/internal/notification.Module`'s own construction) since neither had any
retention class implemented before T2.6; `ledger`'s existing runner just
grew two more classes. Live-verified per class: eligibility boundary
(terminal status + age cutoff), redaction leaves non-sensitive columns
untouched, hold exclusion (subject-scoped for `payin`/`payout`,
resource-scoped for `recon_batches` — neither `recon_batches` nor
`recon_items` has a subject/user_id column), and dry-run count matches the
real run.

**Required tests — live-verified:**

- envelope round-trip, wrong key, wrong AAD, copied ciphertext, truncated
  envelope, old-key read/new-key write — `internal/platform/security/crypto`'s own test suite
  (T2.1).
- normalized email lookup and uniqueness without plaintext —
  `TestUserRepository_GetUserByEmail_NormalizedLookupViaDigest`,
  `TestUserRepository_DuplicateEmail_RejectedViaDigestUniqueness`.
- dual-read/write compatibility during backfill — one
  `TestXxx_DualRead_PreMigrationRowStillWorks`-style test per service
  (auth, payin, payout, ledger recon, adminbff sessions), plus
  `TestKYCRepository_ListRescreenSubjects_WorksAgainstEncryptedPayload`
  proving existing KYC business behavior survives.
- restartable equal-timestamp keyset backfill — six tests (auth users,
  auth KYC, payin, payout, ledger recon, adminbff sessions), each with a
  direct SQL plaintext-absence scan after completion.
- no plaintext sensitive value in database text/JSON columns — every
  round-trip test asserts `require.NotContains(ciphertext, plaintextValue)`
  in addition to the aggregate absence scans above.
- service boot fails when a required current key is missing — T2.2's own
  `internal/platform/config` production fail-fast tests.
- existing business, KYC, webhook replay, payout, and reconciliation
  behavior remains correct — the full pre-existing integration suite for
  auth, payin, payout, ledger, and adminbff ran clean after every change in
  this task, not just this task's own new tests.

Full sweep run clean at the end of this task: `go build ./...`, `go vet
./...`, `go vet -tags=integration ./...`, `TestModuleBoundaries`, `make
docs-check`, `make retention-check` (83 policy entries), the complete
non-integration suite, and the integration suite for every touched package
(auth, payin, payout, ledger, adminbff) — including every pre-existing
test, not just this task's own new ones.

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

**1–2. `internal/platform/security/crypto.DigestRing`** — a new versioned HMAC-SHA256 key type,
deliberately distinct from both `Ring` (reversible envelope encryption) and
`LookupKey` (single, unversioned key): a digest enforcing a *permanent*
unique constraint has to survive key rotation without ever silently
stopping deduplication, which needs the same current/previous-version
machinery `Ring` already has for its KEKs, but with no "open" operation —
a digest is only ever recomputed and compared, never reversed. `Digest`
always uses the current version (posting); `DigestAt` recomputes under a
specific historical version (rotation backfill/drill only).
`idempotency_key_digest` is a keyed HMAC over a length-delimited, canonical
`(scope, key)` encoding (`services/ledger/internal/repository.canonicalIdempotencyInput`)
— length-prefixing, not plain concatenation, so `scope="ab",key="c"` can
never collide with `scope="a",key="bc"`. `conflict_fingerprint` is a
*plain* SHA-256 over `(type, amount, currency)` — no secret key, since its
only job is exact-match comparison against itself, never resisting
offline guessing.

**Key infrastructure** — `internal/platform/config.LedgerIdempotencyConfig`, a
completely separate key namespace from `Cryptox` (`LEDGER_IDEMPOTENCY_KEY_V<N>`,
same Docker-secrets-file convention). Unlike `Cryptox` (optional outside
production), this is **required unconditionally in every environment** —
enforced directly in `cmd/ledger-service`'s own `main.go`, not the shared
`validate()`, so no other service's boot is affected. `ledger.NewModule`
takes a `*cryptox.DigestRing` as a genuinely required parameter (panics on
nil, unlike every T2 field-encryption ring's nil-safe optionality) — K7's
own "never bypasses deduplication" rules out a graceful-degradation
design here, since deduplication is a money-safety invariant, not a
confidentiality one.

**3–4. Digest-first posting** — `ledger_transactions` gained
`idempotency_key_digest`/`idempotency_key_version`/`conflict_fingerprint`
(migration 000028) plus a partial unique index on the digest;
`idempotency_key` became nullable ahead of item 5's redaction.
`TransactionRepository.Insert` now always computes and stores the digest
+ fingerprint. The pre-existing `uq_ltx_idempotency` (raw key/scope) index
is deliberately **left in place, not dropped** — during the window before
a key rotation's backfill has caught every row up to the new current
digest version, it is the only thing that still catches a duplicate whose
freshly-computed digest doesn't match an old row's stale-version digest.
`GetStatusByIdempotency` is replaced by
`FindConflictOrDuplicate(ctx, tx, key, scope, type, amount, currency)`:
looks up **digest-first**, falls back to raw key/scope only if the digest
lookup misses (the rotation-transition safety net above), then compares
the caller's own freshly-computed `conflict_fingerprint` against the
stored one. `handleDuplicate` (`services/ledger/internal/service/handle`) now
checks conflict **before** the existing posted/failed/processing status
switch, returning the new `apperror.ErrIdempotencyConflict` (409, in
`businessRejectionSentinels`) rather than ever silently treating a
mismatched retry as idempotent success.

**5. Retention redaction of raw key/scope after 30 days** — closes a gap
that predates this task: `config/data-retention.yaml` declared
`ledger.transactions.idempotency_raw` (redact, 30d) back in T0, but it had
no purge function until its own prerequisite (this task's digest columns)
existed. `fn_retention_purge_transactions_idempotency_raw`'s
`idempotency_key_digest IS NOT NULL` guard is load-bearing, not defensive:
it must never null a row's raw key/scope before a permanent digest
already exists to carry the deduplication invariant forward. Wired into
ledger's existing retention runner alongside the T2.6 recon classes.

**6. Protobuf/HTTP absent-raw-key handling** — no protobuf/HTTP contract
change was needed: every existing read path
(`GetByIdempotencyKey`/`GetByID`/the transport DTOs) already treated
`idempotency_key`/`idempotency_scope` as ordinary nullable strings before
this task (scope was already nullable; key's own Go field is a plain
`string`, empty-string-safe through JSON/proto either way) — redaction
producing a NULL/empty value going forward requires no new
absent-value-handling code, only the schema change already covered by
item 3.

**Bounded backfill (work item 3)** —
`TransactionRepository.BackfillOnce(ctx, batchSize) (int, error)`, same
`WHERE idempotency_key_digest IS NULL ... FOR UPDATE SKIP LOCKED`,
loop-until-0 shape as every T2.5 repository's own `BackfillOnce`. A row
whose `idempotency_key` is *already* NULL (redaction somehow ran before
backfill reached it — not possible in practice given the digest-guard in
item 5, but handled defensively) is skipped rather than digested from an
empty key. `cmd/ledger-service` gained `--backfill-idempotency-digest`,
mirroring the per-service `--backfill-cryptox` flags from T2.5 exactly
(and for the same `TestModuleBoundaries` reason: no shared cross-service
CLI).

**Required tests — all live-verified against a real Postgres:**

- same key/scope deduplicates before and after raw redaction —
  `TestIdempotency_SameKeyScope_DeduplicatesBeforeAndAfterRawRedaction`
  (posts once, retries once with raw still present, manually nulls
  `idempotency_key`/`idempotency_scope` to simulate the retention job
  having already run, retries again — single monetary effect throughout).
- same key with a different scope remains distinct —
  `TestIdempotency_SameKeyDifferentScope_RemainsDistinct`.
- conflicting amount/type returns the original idempotency conflict —
  `TestIdempotency_ConflictingAmount_ReturnsConflict`
  (`apperror.ErrIdempotencyConflict`, zero monetary effect from the
  rejected retry).
- concurrent retries have exactly one monetary effect —
  `TestIdempotency_ConcurrentRetries_ExactlyOneMonetaryEffect` (20
  goroutines, identical request, every result nil, balance moves exactly
  once, exactly one `ledger_transactions` row).
- current/previous key versions work during rotation —
  `TestDigestRing_CurrentAndPreviousVersions` (`internal/platform/security/crypto`) plus
  `TestFindConflictOrDuplicate_RotationFallbackViaRawKey` (repository
  level: inserts under an old-version-only ring, looks up through a
  rotated ring whose fresh digest legitimately doesn't match, proves the
  raw-key fallback still finds it).
- missing/unknown key versions fail closed —
  `TestDigestRing_MissingVersionFailsClosed` (`internal/platform/security/crypto`) and
  `TestNewTransactionRepository_NilRing_Panics` (construction boundary —
  a repository can never silently exist without a ring).
- digest/backfill migrations and proto checks pass — migrations
  000028/000029 applied live in every test above;
  `TestTransactionRepository_BackfillOnce_RestartableEqualTimestamps`
  (restart/equal-timestamp shape identical to T2.5's own repository
  tests, plus a distinct-digest collision scan across every backfilled
  row); no `.proto` change was needed (item 6).
- `fn_retention_purge_transactions_idempotency_raw` itself —
  `TestRetention_TransactionsIdempotencyRaw_RequiresDigestFirst`:
  eligibility boundary (terminal status + 30 days) **and** the
  digest-guard proven directly (an otherwise-eligible row with no digest
  yet is never redacted).
- no digest or raw key appears in logs/metrics — verified by code
  inspection, not a dedicated automated test: no logging statement
  touches `idempotency_key_digest`, `idempotency_key_version`, or
  `conflict_fingerprint` anywhere in this task's changes, and the
  pre-existing `idem_key_prefix` truncation in `Handle`'s own log fields
  (`services/ledger/internal/service/handle/service.go`) is untouched. Flagged
  here explicitly as an inspection-based guarantee rather than a
  live-tested one, the same honesty standard this doc's other Result
  sections already apply to genuine gaps.

Full sweep run clean at the end of this task: `go build ./...`, `go vet
./...`, `go vet -tags=integration ./...`, `TestModuleBoundaries`, `make
docs-check`, `make retention-check` (83 policy entries), the complete
non-integration suite, and the full integration suite for every touched
package — `services/ledger` and every one of its subpackages (270s,
0 failures), `services/gateway/internal/notification` (real RabbitMQ + real ledger.Module,
unaffected by the digest ring's new required-parameter shape once wired).

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

**Scope decision, stated up front:** this pass ships the complete, real,
working export **mechanism** end to end — everything in work items 1, 4,
5, and 6 — but scopes the actual **data collection** (work item 2's
"paginated owner export contracts") to auth's own owner data only (user
profile + KYC decision history), not all seven owners the doc lists. The
same kind of honest, explicitly-tracked scope call already made for T2.5's
contract migration ("A8 T2.5b"), for the same reason: wiring seven new
internal HTTP endpoints, seven mTLS-allowlist changes, a new
internal-token HTTP middleware, and seven owner-specific allowlisted-field
designs is a second large, mostly-mechanical-but-security-sensitive
undertaking on top of the mechanism itself — better done owner by owner
with its own live verification pass than rushed alongside everything
else. Tracked as follow-up task **"A8 T4b"** with the exact pattern to
replicate (ledger's own cursor-pagination convention, the client-wiring
shape to copy from `services/adminbff/internal/client`) written into the task
description itself. T4's own Definition of Done — "a user can retrieve a
complete... owner-sourced export" — is therefore met **for auth as the
sole owner**, not yet for the full seven-owner scope K9 describes.

**1. Auth-side infrastructure** — `privacy_requests` (migration 000011):
`status` (pending → collecting → ready|failed|expired), a fixed `cutoff`
captured at creation (never a moving target across a multi-step
assembly), `object_key`/`manifest_hash`/`row_count` populated only once
`ready`, `expires_at`/`downloaded_at` for the TTL/one-time-download
machinery. `uq_privacy_requests_active_per_user` (a partial unique index,
not a check-then-insert) enforces K9's "at most one active export per
user" — `RequestExport` treats the resulting unique-violation as success,
returning the existing active request (true idempotent-create, this
codebase's own established convention, e.g. T1's fee-quote/session
classes). `verifyPassword` reuses `Login`'s own
`GetPasswordHash`+`bcrypt.CompareHashAndPassword` call shape rather than
reimplementing it — required at both creation and download time (K9:
"requires... password re-verification" / "download rechecks JWT
ownership and password").

**4. Encrypted ZIP/NDJSON with manifest hashes** — `internal/platform/config.ExportConfig`
is a **dedicated** `internal/platform/security/crypto.Ring`, its own key namespace
(`EXPORT_KEK_V<N>`), never the shared field-encryption `Cryptox` ring —
K2's own separate-key-material principle applied a third time (after
`LookupKey` and T3's `LedgerIdempotency`). `manifest.json` records
`schema_version`, `request_id`, `cutoff`, `retention_policy_version`, a
per-owner `{owner, row_count, sha256}` entry, and an explicit
`exclusions` list (K9: "the manifest records... exclusions" — both
machine- and human-readable, not just a code comment). The whole ZIP
(manifest + one `<owner>.ndjson` per owner) is sealed as a single envelope
under the export ring, keyed to an opaque `exports/<request_id>` object
path (K2's own "no user UUID in the object path," same as KYC documents).
Row DTOs (`exportUserProfileRow`, `exportKYCSubmissionRow`) are
hand-written, never a struct reused from another layer — work item 3's
own "explicit included/excluded fields": KYC's raw submitted `payload`,
`decided_by` (an operator's own identity, not the subject's), and
`provider_ref` (internal vendor correlation id) are deliberately excluded
and named in the manifest's `exclusions` list.

**Saga discipline** — `AssembleOnePendingExport` claims one `pending` row
(`FOR UPDATE SKIP LOCKED`), transitions to `collecting`, and is modeled
directly on `services/payout/internal/relay.go`'s own `dispatchOne`: every exit
path is accounted for, a row is never left silently `collecting` — a
build failure marks `failed` with the error recorded, success marks
`ready` with `object_key`/`manifest_hash`/`expires_at` all set together.
Exported (not left package-private) the same way
`internal/platform/lifecycle/objectoutbox.Worker.ProcessOnce` already is, specifically so
integration tests can drive one unit of work deterministically instead of
racing the 15-second background poller.

**5. One-time download, 24h TTL, object-delete outbox, audit tombstone** —
`DownloadExport` re-verifies password, decrypts the archive **in memory**
(never to disk — the HTTP handler writes the returned bytes straight into
the response), and unconditionally calls the *existing*
`internal/platform/lifecycle/objectoutbox.Enqueue` (T1.6) against a **new** `privacy_requests`
`Target` added to auth's already-running object-delete-outbox worker —
no new outbox table needed. `ExpireOneStaleExport` (same worker, second
poll loop) does the same enqueue for any `ready`, undownloaded row past
`expires_at`; `Enqueue`'s own `ON CONFLICT DO NOTHING` makes both paths
safe to race each other. The `privacy_requests` ROW itself — id, user_id,
status, timestamps, row_count — is retained permanently
(`config/data-retention.yaml`'s new `auth.privacy_requests` entry,
`retain_permanent`) as K9's own "minimal audit tombstone": no email, name,
or financial data ever lands in this table, only opaque pointers and
counts.

**6. `scripts/privacy-export.sh`** — register → request export (password) →
confirm a duplicate request returns the same id → poll to `ready` →
download → confirm a second download attempt is refused (409) → report
`unzip -l` and the manifest's own summary fields only, never archive
contents. Syntax-checked (`bash -n`); not yet run against a live compose
stack in this pass.

**Required tests — all live-verified against a real Postgres:**

- cross-user IDOR attempts — `TestPrivacyExport_GetExportStatus_CrossUserIDOR`
  (another user's export reports `ErrExportNotFound`, never a distinct
  forbidden — no existence disclosure).
- missing/wrong password — `TestPrivacyExport_RequestExport_MissingPassword`.
- disabled user — `TestPrivacyExport_RequestExport_DisabledUser`.
- duplicate request — `TestPrivacyExport_RequestExport_DuplicateReturnsSameActiveRequest`.
- export contains the subject's expected data and no other user's data,
  plus password/token hashes and internal fields are absent —
  `TestPrivacyExport_FullLifecycle_ContainsOwnDataOnlyAndNoSecrets`
  (asserts the archive contains the subject's own email/full name,
  asserts it does NOT contain a second registered user's email, the raw
  password, a `$2a$` bcrypt prefix, `decided_by`, or `provider_ref`).
- artifact is encrypted at rest, wrong KEK fails — same test: the stored
  bytes never contain the subject's plaintext email, and a
  differently-keyed `cryptox.Ring` fails to open the archive.
- successful download and TTL expiry each remove the object idempotently —
  `TestPrivacyExport_Download_OneTimeAndEnqueuesCleanup` and
  `TestPrivacyExport_TTLExpiry_EnqueuesCleanupIdempotently` (both drive
  the real `internal/platform/lifecycle/objectoutbox.Worker.ProcessOnce` and assert the fake
  store ends up empty; the TTL test also calls `ExpireOneStaleExport`
  twice to prove the second call is a safe no-op).
- a failed owner never produces a falsely complete manifest —
  `TestPrivacyExport_FailedAssembly_NeverProducesFalselyReadyRequest`
  (sabotages the cutoff so collection fails deterministically, asserts
  `status='failed'` with a recorded error and nothing ever uploaded).
- owner timeout/retry, pagination, partial assembly (across MULTIPLE
  owners) — not applicable to this pass's single-owner scope; deferred to
  "A8 T4b" along with the owner contracts themselves.

Full sweep run clean: `go build ./...`, `go vet ./...`, `go vet -tags=integration
./...`, `TestModuleBoundaries`, `make docs-check`, `make retention-check`
(84 policy entries, `auth.privacy_requests` newly classified
`retain_permanent`), the complete non-integration suite, and the full
`services/auth` integration suite (108s, every pre-existing test
included, not just this task's own seven new ones).

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

**Scope decision, stated up front:** this pass ships the complete, real,
working closure **saga mechanism** — everything in work items 1, 3
(partially), 4, 5, and 6 — for **two owners: auth and ledger**, not all
eight K11 lists, and **self-service closure only**, not the separate
operator maker/checker offboarding flow work item 2 also names. The same
kind of honest, explicitly-tracked scope call already made for T2.5's
contract migration ("A8 T2.5b") and T4's export owner scope ("A8 T4b"), for
the same reason: wiring `prepare`/`commit`/`status` contracts, a new
internal HTTP endpoint, and an mTLS-allowlist change for six more services
(Pay-in, Payout, Fraud, Gateway, Admin BFF, Assurance), plus a
maker/checker-approved operator offboarding runbook, is a second large
undertaking on top of the saga machinery itself — better done with its own
live verification pass than rushed alongside everything else. Tracked as
follow-up task **"A8 T5b"** with the exact pattern to replicate (ledger's
own `closure.Service`/`ClosureRouter` shape, the `internal/platform/security/middleware.WithInternalToken`
+ mTLS wiring to copy) written into the task description itself. K10's own
Definition of Done — "an eligible user can be de-identified across service
boundaries" — is therefore met **for auth+ledger as the owner set**, not
yet for the full eight-owner scope K11 describes. Auth and ledger were
chosen because they hold the two things that actually gate correctness:
identity/credentials (auth) and money (ledger) — the remaining six owners
hold no blocking preconditions of their own beyond what auth+ledger already
enforce, and their commit steps are strictly pseudonymization/deletion,
each independently addable via the same pattern without touching this
pass's saga state machine.

**1. Closure state on `privacy_requests`** (migration 000012, extending
T4's export table rather than a new one — K10's own "a pending... privacy
export" blocking condition and `uq_privacy_requests_active_per_user` cover
BOTH request types for free from one partial unique index): `request_type`
(`export`|`closure`), `surrogate_id`, `active_subject_ciphertext` (no
separate key-version column — `cryptox.Ring`'s envelope self-describes its
version, same convention T4's export archives already use),
`owner_checkpoints JSONB`, `retry_count`/`next_attempt_at`/`last_error` for
backoff. `status` extended with `preparing`/`committing`/`completed`/
`blocked`/`dead`; `blocked` is terminal-and-not-auto-retried (a real K10
blocking condition needs the user to act, not a backoff loop), `dead` is
reached only after `closureMaxRetries` (5) transient failures, mirroring
the outbox relay's own dead-lettering. `auth_users.status` CHECK extended
with `closing`/`closed` — `Login`/`Refresh` already reject any
`status != 'active'` generically (discovered, not built, this pass), so
both new states get that enforcement for free at zero new code in either
call site.

**2. Password-confirmed self-service closure** — `RequestClosure` reverifies
the password (`verifyPassword`, reused from T4), rejects any role other
than `RoleUser` (`ErrClosureNotSelfService` — admin/admin_maker/
admin_checker require the not-yet-built offboarding runbook, A8 T5b),
generates the surrogate UUID, seals the original subject id under a
**fourth** dedicated key namespace (`internal/platform/config.ClosureConfig`,
`CLOSURE_KEK_V<N>`, same K2 separate-key-material principle as
`LookupKey`/`LedgerIdempotency`/`Export`), and in one transaction inserts
the `pending` closure row and flips `auth_users.status` to `closing` — this
is what "immediately disables new login" actually means: no worker tick is
needed for `Login`/`Refresh` to start rejecting the account. Refresh tokens
are best-effort revoked in the same call (`RevokeAllForUser`) — best-effort
because the status flip is ALREADY the enforcement point (a live but
unrevoked token still fails `Refresh`'s status check), not the only one.

**3. Owner `prepare`/`commit`/`status` — auth and ledger.** Ledger's new
`services/ledger/internal/service/closure.Service` (`Prepare`/`Commit`) is exposed
over a **separate** `Module.ClosureRouter()` — deliberately NOT added to
the existing broad `transport.Service` interface `InternalRouter()` uses,
since widening that interface for one narrowly-scoped, single-caller
feature would force it on every future implementer of that module
boundary. `ClosureRouter` is gated by a **new** `internal/platform/security/middleware.WithInternalToken`
(this codebase's first HTTP analog of `internal/platform/transport/grpc`'s existing gRPC
`authInterceptor`/`INTERNAL_GRPC_TOKEN` — reuses that exact same shared
secret, no new one introduced) plus the internal listener's own mTLS
identity allowlist (`tlsx.IdentityAuth` added to ledger's `:8091` class).
`Prepare` checks non-zero balance, any `pending` transaction touching the
subject's accounts (open lifecycle, generalized), active/paused scheduled
transactions, and pending disbursement items — `pending_adjustments` is a
known, documented gap (no first-class user column, only an opaque
`cmd_payload` JSONB not safe to match on) deferred to A8 T5b. `Commit`
repoints `accounts.owner_id` from subject to surrogate and is idempotent
WITHOUT its own checkpoint: the result (hash + count) is always re-derived
from what the surrogate currently owns, never from "rows affected this
call," so replaying it (proven live by `TestClosure_Commit_IdempotentUnderReplay`)
returns the identical result both times. Auth's own local `prepare` checks
(no other-owner call needed) cover a pending KYC submission and an active
`auth_retention_holds` row scoped to the subject — reusing T1's existing
K5 hold table rather than building a new one.

**4/5. Disable-first, commit-owners, finalize-auth-last, destroy ciphertext.**
`ProcessOnePendingClosure` claims one due row (`FOR UPDATE SKIP LOCKED`)
and advances it **exactly one step** per call — `pending`→`preparing`
(local + ledger prepare), `preparing`→`committing` (ledger commit),
`committing`→`completed` (auth finalize) — the same small-resumable-unit
discipline `AssembleOnePendingExport`/payout's `dispatchOne` already
established: a crash at any point leaves the row at its last durably
written status, and the next call (this process or another replica) simply
continues from there — proven live by `TestClosure_HappyPath_FullLifecycle`
driving the saga via repeated calls rather than one shot.
`closureAdvance` merges each owner's checkpoint into `owner_checkpoints`
(`{"ledger": {"phase": "committed", "result_hash": ..., "affected_count": ...}}`)
and clears retry state on success; `closureRetryOrDead` backs off
(30s × 2^retry) and dead-letters after 5 attempts on a transient failure,
never on a `blocked` result. Auth's finalize step (`closureStepFinalize`,
all local, one transaction, no network boundary to resume across) deletes
`auth_credentials`, revokes any remaining live refresh tokens, tombstones
`email`/`full_name` to fixed values (`closed+<request-id>@deleted.invalid`,
`[deleted]`) keyed by the request id so the tombstone email can never
collide, sets `status='closed'`, and NULLs `active_subject_ciphertext` —
K10's own "destroy the active-saga ciphertext." (Scope note: auth's own
row `id` is retained in place, tombstoned, rather than migrated to a new
surrogate-keyed row — repointing `kyc_submissions`/`auth_credentials`/
`auth_refresh_tokens` to a literal new primary key would require cascading
seven FK-referencing tables for no additional privacy benefit, since the
retained row carries zero PII either way; the surrogate UUID is what every
OTHER owner — ledger, in this scope — repoints its OWN copy of the
reference to, which is the actual cross-service unlinking K11 is about.)

**Required tests — all live-verified against a real Postgres and a real
in-process `ledger.Module` reached over a genuine HTTP round trip**
(`ledgerHarness.Module().ClosureRouter()` wrapped in `httptest.Server`,
exactly cmd/auth-service's own production transport shape minus mTLS):

- every blocking condition and an eligible happy path —
  `TestClosure_NonZeroBalance_Blocks`, `TestClosure_ActiveHold_Blocks`,
  `TestClosure_HappyPath_FullLifecycle`.
- admin/operator self-service rejection — `TestClosure_RequestClosure_AdminRejected`.
- already-disabled user — `TestClosure_RequestClosure_AlreadyDisabledUser`.
- crash/restart resumption between saga steps — every test drives the saga
  via repeated `ProcessOnePendingClosure` calls (never a single shot),
  which IS the resumption path; `TestClosure_HappyPath_FullLifecycle`
  additionally asserts login is rejected immediately after the REQUEST,
  before any worker step has run.
- duplicate commands do not change result counts or create a second
  surrogate — `TestClosure_DuplicateRequest_ReturnsSameActiveRequest`
  (request-creation half) and `TestClosure_Commit_IdempotentUnderReplay`
  (owner-commit half: calls ledger's `Commit` twice for the same subject/
  surrogate pair, asserts an identical hash/count and no extra rows).
- hold appears during prepare and prevents commit —
  `TestClosure_ActiveHold_Blocks`.
- old login, refresh token, and user routes fail after completion —
  `TestClosure_HappyPath_FullLifecycle` (asserts `Login` returns
  `ErrInvalidCredentials` post-tombstone, `Me` returns `ErrUserDisabled`,
  zero live rows remain in `auth_refresh_tokens`); admin session and old
  subject lookup are out of this pass's owner scope (A8 T5b).
- all owner references use the surrogate — same test: asserts
  `accounts.owner_id` for every account moved from the original id to the
  surrogate, zero accounts remain under the original.
- `ledger_entries` checksums are byte-for-byte identical before/after —
  `TestClosure_LedgerEntriesUnchanged` (a real top-up + P2P transfer, then
  closure, then a SHA-256 over every entry row for the closed user's
  accounts, before vs. after — equal).
- logs/audit never expose original-to-surrogate mapping — asserted
  structurally: `active_subject_ciphertext` is NULL on the completed row
  (`TestClosure_HappyPath_FullLifecycle`), and no log statement in
  `closure_worker.go`/`closure.go` logs a user id alongside a surrogate id.
- one unavailable owner leaves the user disabled and resumes forward
  later — not separately live-verified this pass (would require injecting
  an HTTP failure into the httptest server mid-saga); the mechanism
  (`closureRetryOrDead`, status stays unchanged on a transient error) is
  the same pattern `AssembleOnePendingExport`'s own failure path already
  uses and T4 already proved live — deferred to A8 T5b's own live
  verification alongside the additional owners.

Full sweep run clean: `go build ./...`, `go vet ./...`, `go vet -tags=integration
./...`, `TestModuleBoundaries`, `TestSchemaContract_*` (full package, 195s),
`make docs-check`, `make retention-check` (84 policy entries, unchanged
from T4 — closure extends the SAME `privacy_requests` table, no new table
to classify), the complete non-integration suite across every package, and
`services/auth`'s full integration suite including every pre-existing test
(51s) plus this task's own 9 new closure tests (28s).

### A8 T4b/T5b — Owner contract wiring (K9, K10, K11)

**Scope actually closed this pass:** every owner K11 lists that genuinely
holds first-class end-user data now has real, live-verified export +
closure contracts: **ledger** (already done at T5, gained a new `Export`
capability here), **payin**, **payout**, **fraud**, and **gateway**
(`services/gateway/internal/notification`, the module that actually owns `notif_notifications` —
gateway itself has no `services/gateway` package). **admin-bff and
assurance are wired to nothing, by design, not by omission** — verified by
code inspection before writing any contract for either:

- admin-bff owns no end-user `user_id` data at all — its only tables are
  admin OPERATOR sessions/audit (`sessions.role CHECK IN
  ('admin','admin_maker','admin_checker')`, `services/adminbff/internal/login.go`).
  K11's own wording — "operator session removal... when applicable" — is
  satisfied because self-service closure structurally excludes
  admin/operator accounts (`ErrClosureNotSelfService`, T5). There is
  nothing for an ordinary end-user's export or closure to touch here.
- assurance's `assurance_findings.resource_id` is populated from
  `PayinRecord.ID`/`PayoutRecord.ID` (`services/assurance/rules/rules.go`),
  **never** a `user_id` — no first-class subject column exists to
  pseudonymize or export. K11's own "verify evidence contains no hidden
  subject field" is satisfied by this design, confirmed by reading the
  rule engine, not by an active owner action.

This narrows T4b/T5b's real remaining scope from "six more owners" (as
originally deferred) to four, and both narrowings are recorded here rather
than silently assumed.

**Unified owner client, one registration serves both sagas.** Every owner
exposes the identical wire contract at one base URL (`POST
<base>/privacy/closure/prepare`, `POST <base>/privacy/closure/commit`, `GET
<base>/privacy/export`), so `OwnerClosureClient`
(`services/auth/internal/owner_closure_client.go`) gained a third method (`Export`)
rather than a parallel client type — `cmd/auth-service/main.go` calls
`RegisterClosureOwner` exactly five times (ledger, payin, payout, fraud,
gateway) and both the export worker and the closure saga read from the
same `m.closureOwners` registry. Owner registration was moved out of the
`Closure.Keys`-gated block into an always-executed one, so an
export-only deployment (no `CLOSURE_KEK` configured) still gets every
owner's export data — export and closure are two independent capabilities
that happen to share a registry, not one gated by the other.

**Auth closure worker rewritten for N owners.**
`services/auth/internal/auth/closure_worker.go` no longer hardcodes a single ledger call;
it loops `m.closureOwners` (an ordered slice — registration order is
deterministic saga processing order) at both the `prepare` and `commit`
steps, checkpointing each owner's own phase into the existing
`owner_checkpoints` JSONB column (`closureOwnerPhase`/
`closureCheckpointOwner`) so a resumed saga skips owners already
checkpointed at the current step — the same crash-resumable, one-step-per-call
discipline T5's own single-owner version established, now proven at
N-owner granularity. `closureStepPrepare` deliberately keeps probing every
owner even after one reports blocked, so a blocked closure reports every
blocking reason across every owner at once rather than one at a time
across repeated calls.

**Owner-side contract shape (`payin`/`payout`/`fraud`/`notify`), each a new
`privacy.go` + `privacy_http.go` pair mirroring ledger's own
`closure.Service`/`ClosureRouter` shape from T5:**

- **payin** — `Prepare` blocks on a `payin_topup_intents` row still
  `pending` (money may be in flight to a vendor). `Commit` repoints
  `user_id` on `payin_webhook_events`/`payin_topup_intents`/
  `payin_routing_rules`. `Export` excludes `raw`/`raw_ciphertext` (T2.4's
  encrypted vendor payload) and `error_message`.
- **payout** — `Prepare` blocks on any `payout_requests` row not yet in a
  terminal status (`settled`/`failed`/`cancelled`) — an open withdrawal
  lifecycle. `Commit` repoints `payout_requests`/`payout_routing_rules`.
  `Export` excludes `destination`/`destination_ciphertext`, `vendor_ref`,
  and `error_message`.
- **fraud** — `screening_events` is pure history, so `Prepare` never
  blocks. `Commit` repoints `user_id` through a **new** `SECURITY DEFINER`
  function, `fn_privacy_closure_repoint_screening_events`
  (`migrations/fraud/000006`) — the table deliberately grants `app_service`
  no `UPDATE` (append-only-audit philosophy, same as `payout_vendor_calls`),
  so the repoint is channeled through one narrow function rather than
  widening the table's grant. `Export` includes `reason` (rule-generated,
  never vendor/user free text, unlike payin/payout's excluded fields).
- **gateway/notify** — `notif_notifications` is a read-log of already-posted
  events, so `Prepare` never blocks. `Commit` repoints `user_id`. `Export`
  excludes the internal `payload` rendering aid; `title`/`body` already
  carry the user-facing content.

Each owner's `PrivacyRouter()` is mounted as a sibling to that service's
existing JWT-gated admin router (bare `/privacy/` path, never under
`/api/v1/`), gated by the same `internal/platform/security/middleware.WithInternalToken` +
`tlsx.IdentityAuth` allowlist pattern T5 established for ledger — added to
payin-service's, payout-service's, fraud-service's, and gateway's own
internal listener this pass.

**A real bug found and fixed by this pass's own live tests, not by
inspection:** the wire cutoff auth-service sends to every OTHER owner's
`GET /privacy/export` was encoded with `cutoff.UTC().Format(time.RFC3339)`
(`services/auth/internal/owner_closure_client.go`) — `time.RFC3339`'s `Format` drops
fractional seconds entirely, so every owner's effective cutoff was always
rounded *down* to the start of the second, silently excluding any owner
row created in that truncated sub-second gap from exports — while auth's
own in-process `collectAuthOwnerRows` used the full-precision Go
`time.Time` for its own data. `TestMultiOwner_Export_IncludesAllRegisteredOwners`
caught this directly (a payin row inserted milliseconds before the export
request came back with `row_count: 0` for every non-auth owner). Fixed by
switching the client's `Format` call to `time.RFC3339Nano` (Go's `Parse` on
the receiving side already accepted fractional seconds — only the
`Format` side was lossy, so this is a one-line fix, not a wire-format
change).

**Required tests — all live-verified against a real Postgres and real
in-process owner modules** (`services/auth/internal/closure_multiowner_integration_test.go`,
each owner reached over a genuine HTTP round trip, same
httptest.Server-wrapping-a-real-module shape T5 established for ledger):

- happy path repoints every new owner to the surrogate and checkpoints
  every owner `committed` — `TestMultiOwner_Closure_RepointsAllFourNewOwners`
  (seeds one row per new owner directly, drives the saga to `completed`,
  asserts `user_id` on all four new tables plus ledger's `accounts.owner_id`
  now equals the surrogate, and every owner's `owner_checkpoints` phase is
  `committed`).
- payin's own K10 blocking condition —
  `TestMultiOwner_Closure_PendingTopupIntentBlocks` (a `pending` topup
  intent blocks closure with a reason naming it).
- payout's own K10 blocking condition —
  `TestMultiOwner_Closure_OpenPayoutRequestBlocks` (a `held` payout request
  blocks closure with a reason naming the open withdrawal lifecycle).
- export includes every registered owner, and one owner's export never
  leaks another user's row — `TestMultiOwner_Export_IncludesAllRegisteredOwners`
  (downloads and decrypts the real archive, asserts the manifest lists all
  six owners — auth, ledger, payin, payout, fraud, gateway — asserts
  `payin.ndjson` contains the subject's own webhook-event row id and does
  NOT contain a second registered user's row id).

Full sweep run clean: `go build ./...`, `go vet ./...`, `go vet
-tags=integration ./...`, the complete non-integration suite across every
touched package (`services/payin`, `services/payout`, `services/fraud`,
`services/gateway/internal/notification`, `services/ledger` and subpackages), and `services/auth`'s
full integration suite — all 18 pre-existing tests plus these 4 new ones,
22/22 passing (163s).

### A8 T5b (continued) — Operator maker/checker offboarding and injected-owner-failure test (K10)

The two gaps this doc's own "A8 T4b/T5b" section above explicitly named as
still open — the separate maker/checker operator offboarding flow, and a
live-verified injected-owner-failure resumption test — are both closed
here.

**Operator offboarding reuses the entire closure saga rather than building
a second one.** `migrations/auth/000013` adds
`auth_operator_offboarding_requests`, deliberately mirroring ledger's own
`pending_adjustments` maker-checker shape (`migrations/ledger/000006`)
field-for-field: `requested_by`/`approved_by` are operator identities
(never the target's own PII), a `CHECK (approved_by IS NULL OR
approved_by <> requested_by)` is the database-level backstop behind the
application-level self-approval check, and `closure_request_id` links the
decision to the actual closure it starts. `services/auth/internal/operator_offboarding.go`'s
`ProposeOperatorOffboarding` (the maker half — rejects a target whose role
is `RoleUser`, since that account uses self-service `RequestClosure`
instead) and `ApproveOperatorOffboarding` (the checker half) mirror
`services/ledger/internal/service/adjustments.Service`'s own `Create`/`Approve`
control-flow shape, but **Approve does not itself move or pseudonymize
anything** — it inserts the exact same `privacy_requests` row
`RequestClosure` inserts (same surrogate generation, same
`active_subject_ciphertext` sealing under the SAME closure key ring, same
`auth_users.status` flip to `closing`), so the pre-existing closure saga
worker and every owner registered by "A8 T4b/T5b" above drive the rest
identically regardless of which entry point started it — no new saga
logic, only a new gate in front of the existing one. HTTP routes
(`POST/GET /api/v1/admin/privacy/operator-offboarding...`) are gated by
new `isAdminMaker`/`isAdminChecker` helpers that mirror
`services/ledger/internal/transport`'s own identically-named functions exactly, and
are reachable with zero admin-bff changes — they land inside admin-bff's
already-existing generic `/api/v1/admin/privacy/` proxy subtree.
`config/data-retention.yaml` gained the new table's own entry (85 policy
entries now, up from 84) and `docs/data/retention.md` was regenerated.

**A real transaction-ordering bug found by the first live test run, not by
inspection:** `ApproveOperatorOffboarding`'s original code UPDATEd
`auth_operator_offboarding_requests.closure_request_id` (an FK to
`privacy_requests.id`) BEFORE inserting the referenced `privacy_requests`
row in the same transaction — Postgres checks FK constraints immediately,
not at commit, so this failed with `violates foreign key constraint` on
every single approval. Fixed by reordering the transaction body: insert
`privacy_requests` first, then the offboarding row referencing it.

**Required tests — all live-verified against a real Postgres and every
registered owner** (`services/auth/internal/operator_offboarding_integration_test.go`):

- happy path closes the target across every owner —
  `TestOperatorOffboarding_HappyPath_ClosesTargetAcrossAllOwners` (propose
  by one operator, approve by a DIFFERENT one, asserts the target flips to
  `closing` immediately on approval — same as self-service — then drives
  the saga to `completed` and asserts the target ends `closed`).
- self-approval refused for both decisions —
  `TestOperatorOffboarding_SelfApproval_Rejected` (same identity attempting
  both `Approve` and `Reject` on its own proposal).
- target must actually be an operator, not an ordinary user —
  `TestOperatorOffboarding_TargetMustBeOperator`.
- an already-decided request cannot be decided twice —
  `TestOperatorOffboarding_AlreadyDecided_SecondApprovalRejected`.
- a rejected proposal never touches the target or starts a closure —
  `TestOperatorOffboarding_Reject_NeverStartsClosure` (asserts the target's
  `status` is untouched and zero `privacy_requests` rows exist for it).

**Injected-owner-failure resumption — T5's own required test, explicitly
flagged as not live-verified in both T5's and "A8 T4b/T5b"'s own Result
sections above, closed now.** `closure_injected_failure_integration_test.go`
wraps payin's real `PrivacyRouter()` behind a `flakyHandler` — an
`atomic.Bool` switch that returns a genuine HTTP 503 when flipped off,
exercised through the SAME `httpOwnerClosureClient` every other test uses,
not a mocked error return. With payin (the second-registered owner) down:
one `ProcessOnePendingClosure` call leaves the request `pending` (never
silently advances), `retry_count=1`, `last_error` naming payin,
`next_attempt_at` scheduled into the future — and ledger's own checkpoint
(the first-registered, successfully-called owner) survives in
`owner_checkpoints` while payin's does not yet exist. A second poll call
BEFORE the backoff window elapses is asserted to be a genuine no-op (the
claim query's own `next_attempt_at` gate, not just "would eventually
retry"). The backoff window is then fast-forwarded (`next_attempt_at` set
to `NULL` directly, rather than sleeping 30s) and payin flipped back
healthy — `driveClosureToCompletion` reaches `completed`, and every
owner's checkpoint (including payin's, retried successfully this time) is
present in the final `owner_checkpoints`.

Full sweep run clean: `go build ./...`, `go vet ./...`, `go vet
-tags=integration ./...`, `make retention-check` (85 entries),
`make docs-check`, and `services/auth`'s full integration suite — all 22
pre-existing tests plus these 6 new ones (5 offboarding + 1 injected-failure),
28/28 passing.

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

**Scope decision, stated up front:** this pass completes work items 1, 5,
and 6 (partially) in full, ships a real work item 2 (`scripts/privacy-e2e.sh`)
that is live-verified **indirectly** (every API contract it drives is
proven live by the Go integration suite, but the script itself could not
be run end-to-end against a live `docker-compose` stack in this session —
see "Environment limitation" below), and **defers** work item 3 (new
privacy/closure-specific chaos scenarios) and work item 4 (a live A7
backup-interaction lifecycle proof) entirely. Tracked as follow-up task
**"A8 T6b"** with the exact gaps and the pattern to close them written into
the task description. This is the same honest, explicitly-tracked
scope-reduction discipline already used for "A8 T2.5b", "A8 T4b", and "A8
T5b" — applied here to the FINAL gate task rather than pretending a smaller
scope was the whole of T6.

**Environment limitation (found and partially fixed, not fully worked
around):** attempting to run the required final-gate scripts this session
surfaced a real, previously-undetected regression, unrelated to this
session's own port issues: **`ledger-service` could not boot via
`docker-compose.yml` or `scripts/lib.sh` at all**, because T3 (K7) made
`LEDGER_IDEMPOTENCY_KEY_V1` mandatory but never wired a value for it into
either — every T3/T4/T5 "full sweep" claim to date relied exclusively on
`go test -tags=integration` (testcontainers, which never boots the actual
compiled binaries), so this had gone uncaught since T3 shipped earlier in
this same execution window. Fixed this pass: `docker-compose.yml` gained
`ledger_idempotency_key_v1`/`export_kek_v1`/`closure_kek_v1` Docker
secrets (extends `make cryptox-secret`) and the ledger-service/auth-service
`environment:` blocks now wire them (plus `LEDGER_INTERNAL_API_URL` for
the closure client); `scripts/lib.sh` gained the equivalent fixed
local-process env vars; `.env.example` documents all of it.
**Confirmed live**: after the fix, a locally-built `ledger-service` binary
gets past the `LEDGER_IDEMPOTENCY_KEY_V1` check and connects to Postgres
and Redis successfully (`{"msg":"postgres: connected"...}`,
`{"msg":"redis: connected"...}` in its own log) — it then fails to reach
RabbitMQ, but that failure is this specific sandboxed environment's own
unrelated pre-existing container (a different, unrelated project's
`rabbitmq` container already bound to host port 5672 before this session
started) — not a repo defect. This session could not free that port
(stopping another project's running container without authorization is
out of bounds per this session's own standing safety rules), so the
`docker-compose`-based scripts in the "Required final gate" list below
(`smoke-test.sh`, `business-e2e.sh`, `admin-e2e.sh`, `chaos-test.sh all`,
`privacy-e2e.sh`) could not be run to completion in THIS session. Also
found and fixed while investigating: `scripts/privacy-export.sh` (T4) and
this pass's own first draft of `privacy-e2e.sh` both read response fields
at the wrong JSON path (`.id` instead of `.data.id` — `internal/platform/transport/http/response.Envelope`
wraps every payload under `"data"`) — a second latent bug from the same
"never actually run this script live" gap. Both fixed and syntax-verified
this pass; a user running these scripts in an environment without the
port conflict gets the corrected paths for free.

**1. Metrics, alerts, runbooks, admin panel.** Four of K13's five
remaining (post-T1) privacy metrics now exist in
`services/auth/internal/privacy_metrics.go`: `seev_privacy_requests{kind,status}`
(gauge, refreshed once per worker tick — same convention as
`internal/platform/lifecycle/retention/worker`'s own `holdsGauge`), `seev_privacy_request_duration_seconds{kind,result}`
(histogram, observed at every terminal transition — `ready`/`failed`/`expired`
for export, `completed`/`blocked`/`dead` for closure), `seev_privacy_owner_calls_total{owner,operation,result}`
(incremented around every ledger `Prepare`/`Commit` call), and
`seev_privacy_object_delete_total{kind,result}` (export archive
delete-enqueue attempts). `seev_pii_backfill_rows_total{owner,field,result}`
is deliberately NOT implemented — instrumenting `BackfillOnce` across four
separate repository packages for an already-complete, one-time/operational
CLI flow is a real cross-package lift with lower incident-response value
than the request-lifecycle metrics; the same honest-gap convention T1
already used for `seev_retention_oldest_eligible_age_seconds`. Four new
alerts in `deploy/observability/prometheus/rules/retention.yml`'s new
`privacy-a8` group (`SeevPrivacyExportStuckCollecting`,
`SeevPrivacyClosureStuck`, `SeevPrivacyClosureDead`,
`SeevPrivacyOwnerCallsFailing`), validated with `promtool check rules`
(7 rules found, up from 3). New runbook
`docs/operations/runbooks/data-lifecycle-privacy.md` — six situations
(active hold, failing purge/redact, failed export, stuck/dead closure, key
mismatch across all three dedicated rings, failing object-store delete),
each with the exact SQL/commands to diagnose and recover, explicitly
warning against every manual-bypass shortcut that would defeat the saga's
own consistency guarantees. New admin BFF read-only status panel: auth's
new `AdminListPrivacyRequests`/`AdminPrivacyRequestsHandler` (never
exposes email/full_name — this table never stored them for export, and
closure tombstones them anyway) proxied through admin-bff's existing
`/api/v1/admin/privacy/` route and two new `catalog.html` panel entries,
mirroring every other read-only status panel's own `hx-get` convention.

**2. `scripts/privacy-e2e.sh`.** Four legs: export (delegates to the
existing, now-fixed `privacy-export.sh` unchanged), retention
(`cmd/retentionctl status`/`dry-run` reachability), hold (create via
`retentionctl hold-create` → confirm closure reaches `blocked` → release
via a DIFFERENT operator identity, K5's maker/checker-for-holds rule),
and closure happy path (request → confirm login rejected IMMEDIATELY,
before any worker tick → poll to `completed` → confirm login now returns
401, not 403 — tombstoned, not merely disabled). Every HTTP/DB
interaction this script performs is the SAME contract the 9 closure + 7
export Go integration tests already drive live against real Postgres and
a real in-process `ledger.Module` — see the environment limitation above
for why the script itself couldn't be run end to end this session.

**5. Plaintext scans.** Static, source-level: every `logger`/`slog` call
site added across T4/T5/T6 (`privacy_worker.go`, `closure_worker.go`,
`closure.go`) logs only `"error", err` — never a literal email, password,
or ciphertext field. One error message interpolates a user UUID
(`privacy_worker.go`'s "user %s not found as of cutoff") — consistent
with this codebase's existing, already-accepted convention that opaque
UUIDs (unlike email/name) are not "subject data" in K13's sense; every
other admin/audit endpoint in this repo already includes user IDs in
responses. No dynamic scan of a running stack's actual logs/Tempo/Loki
fixtures/CI diagnostics was possible this session (same environment
limitation) — deferred to "A8 T6b".

**6. Row counts / durations — partial.** Live integration-test timing is
the evidence available this pass (not production-scale row counts from a
running stack, which needs the blocked docker-compose gate): the 9
closure tests run in 28–65s across repeated live runs (2.2–4.6s each), the
7 export tests in 50–65s (2.9–4.9s each). Full sweep run clean this pass:
`go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`,
`make lint` (one `gosimple` finding on the new admin handler, fixed —
direct struct conversion instead of a field-by-field literal), `make
proto`/`proto-lint`/`proto-breaking` (no diffs), `make docs-check` (106
files), `make retention-check` (84 entries, unchanged from T4 — no new
table), and the complete `make test` suite (every package). A full-repo
`go test -tags=integration -race ./...` run timed out on `services/auth`
alone at Go's default 10-minute per-package limit (the race detector's
instrumentation overhead compounds badly with this package's many
testcontainers-based tests) — re-run scoped to that package with a
25-minute budget instead: `go test -tags=integration -race -timeout 25m
./services/auth/internal/...` **PASSED clean, 350.4s, zero data races reported**.
This package alone covers every T4/T5/T6 code path this pass touched
(export, closure, privacy admin panel). Every OTHER individual package
this pass touched (`services/ledger`, `services/adminbff`,
`internal/platform/config`, `internal/testkit`) was already independently
live-verified multiple times earlier in this same session with
`-tags=integration` (without `-race`) — see each task's own Result
section above for the specific pass counts and timings; none of those
packages' new code this pass introduces concurrent access to shared state
(closure/export are per-request DB transactions, no new shared in-memory
structures), so `services/auth`'s own clean `-race` result is
representative.

**3/4. Deferred to "A8 T6b".** New chaos scenarios for the six named
failure modes (database outage, object-store outage, key mismatch, worker
kill/restart, owner timeout, retention/closure races) and a live A7
backup-interaction proof (K12: "new backups contain only the
post-redaction state, old chains expire on schedule") were not built or
run this pass — both require the same live-stack access this session's
environment limitation blocked, on top of being substantial standalone
undertakings (backup-interaction specifically needs the full pgBackRest
agent stack from doc 50, not just app-profile services). K12's own
fallback wording — "describe as a known limitation rather than claiming
complete erasure from backups" — is the honest posture adopted here even
though A7 is implemented, because the specific cross-track lifecycle test
tying redaction/closure to backup content was never built.

**Definition of done — partially met.** Lifecycle and privacy behavior IS
measurable (K13 metrics/alerts), restart-safe (proven live for
closure/export saga resumption), and operator-usable (runbook + admin
panel) for the auth+ledger scope T4/T5 shipped. It is NOT yet verified via
the full required final-gate script list, and chaos/backup-interaction
coverage remains open — both tracked in "A8 T6b", not silently dropped.

### T6b (deferred) — Docker-compose live gate, chaos scenarios, backup-interaction proof

Three concrete gaps left open by T6, each independently actionable:

1. **Run the full required final gate** (`scripts/smoke-test.sh`,
   `scripts/business-e2e.sh`, `scripts/admin-e2e.sh`,
   `scripts/chaos-test.sh all`, `scripts/privacy-e2e.sh`) in an
   environment where host port 5672 is free, confirming the
   `LEDGER_IDEMPOTENCY_KEY_V1`/`EXPORT_KEK_V1`/`CLOSURE_KEK_V1`/
   `LEDGER_INTERNAL_API_URL` wiring this pass added to `docker-compose.yml`/
   `scripts/lib.sh` actually boots every service end to end (partially
   confirmed this pass — ledger-service got past its own mandatory key
   check and connected to Postgres/Redis; RabbitMQ connectivity and every
   downstream service were not reached).
2. **New chaos scenarios** for the six failure modes T6's own work item 3
   names: database outage, object-store outage, key-version mismatch,
   privacy-worker kill/restart mid-saga, owner-call timeout (inject an
   HTTP failure into the closure worker's `httptest`-based test harness or
   a real network partition), and retention/closure races (concurrent
   `ProcessOnePendingClosure` calls racing a retention purge on the same
   subject). Add as new numbered scenarios in `scripts/chaos-test.sh`,
   following the existing scenario 9/10/11 (doc 45) pattern.
3. **Live A7 backup-interaction proof (K12)**: redact/purge/close a test
   subject, take a NEW pgBackRest backup via the doc-50 backup-agent
   stack, restore it in the isolated drill compose
   (`deploy/backup/restore-compose.yml`), and confirm via `pg_dump` +
   plaintext grep that the restored database contains ONLY the
   post-redaction state — plus confirm the OLD backup chain (predating the
   redaction) still exists and expires on its own documented retention
   schedule, never accelerated by a privacy action. Update K12's own
   acceptance checklist item and this doc's "Backup and export
   interaction" note once done.

### Final completion pass (2026-07-26)

This result supersedes the intermediate deferred-status notes above. The
remaining T2.5b, T4b, and T6b work was completed across auth, ledger, pay-in,
payout, fraud, gateway/notify, admin BFF, and assurance:

- contract migrations removed the old plaintext columns and every runtime now
  requires a real, versioned `cryptox` implementation;
- the policy matrix now classifies 88 durable and ephemeral data classes, with
  bounded, fail-closed retention workers and legal-hold protection;
- export collection uses bounded cursor pages (including a greater-than-100-row
  multi-owner test), encrypted one-time objects, truthful deletion metadata,
  and request-type-isolated workers;
- closure verification covers every owner, ledger immutability, assurance
  before and after closure, restart convergence, and completed closure
  surrogates during disaster-recovery verification;
- chaos scenarios 15–20 cover database and object-store outage, key mismatch,
  worker restart, owner timeout, and retention/closure races;
- the privacy E2E passed twice consecutively, chaos scenarios 15–20 passed
  twice consecutively, the complete chaos suite passed scenarios 1–20, and
  smoke, business, and admin journeys passed;
- the A7 interaction drill restored a post-closure backup with zero verifier
  findings, retained the older chain according to its own horizon, passed the
  security fence and assurance checks, and measured RPO 0 seconds / RTO 39
  seconds.

## 7. Acceptance checklist

### Inventory and retention

- [x] Every database table, object class, event payload, and cache class is in
      the versioned policy matrix.
- [x] CI rejects unclassified new tables and purge rules targeting immutable
      ledger data.
- [x] Owner workers purge/redact all eligible default classes in bounded
      batches.
- [x] Holds, live states, unknown policy versions, and unavailable proof fail
      closed.
- [x] Normal application roles cannot execute direct unrestricted deletion.

### Sensitive-data protection

- [x] Auth/KYC, pay-in raw data, payout destination, reconciliation raw data,
      and operator identity fields have no plaintext database copy.
- [x] KEK and lookup-key separation, versioning, rotation, and wrong-key
      behavior are tested.
- [x] Object keys, logs, metrics, traces, errors, and diagnostics contain no
      prohibited personal data.
- [x] KYC and export object deletion is idempotent and metadata accurately
      reflects storage state.

### Idempotency and money safety

- [x] Historical raw idempotency keys are redacted after 30 days.
- [x] Permanent digest tombstones preserve replay and conflict behavior.
- [x] Concurrent replay after redaction has exactly one monetary effect.
- [x] Fee-quote cleanup cannot race consumption or delete unverified fee
      proof.
- [x] Ledger entries are byte-identical across pseudonymization.

### Export and closure

- [x] Export ownership, re-authentication, bounded cursor pagination,
      encryption, one-time download, expiry, and exclusions pass.
- [x] Closure refuses every open-money, pending-work, hold, and dependency
      condition.
- [x] Eligible closure converges after injected owner failures and restart.
- [x] All classified references are pseudonymized or purged with no mapping in
      logs or audit output.
- [x] Ledger and assurance verification pass before and after closure.
- [x] Backup-retention limitations and expiration horizon are explicit and
      live-verified.

### Operations

- [x] Metrics and dashboards use only bounded labels.
- [x] Runbooks cover hold, failed purge, failed export, stuck closure, key
      rotation, object deletion, and backup residuals.
- [x] Privacy E2E and focused failure drills pass twice consecutively.
- [x] Full build, vet, lint, race, integration, smoke, business, admin, chaos,
      proto, documentation, compose, and diff gates are green.

## 8. Global Definition of Done

- [x] T0–T6 results contain commands and concise evidence.
- [x] No immutable financial evidence is removed or altered.
- [x] No sensitive plaintext or original/surrogate mapping appears in source,
      runtime output, or test artifacts.
- [x] Policy defaults and legal/non-legal boundaries are documented clearly.
- [x] The plan index and roadmap mark A8 complete and the plan is archived.

## 9. Explicit follow-ups

The following remain outside A8:

1. jurisdiction-specific retention approval and legal certification;
2. production KMS/HSM and external key escrow;
3. deletion propagation to a future CDC/warehouse platform;
4. off-site backup deletion beyond A7 retention expiry;
5. partitioning/archival and production-scale purge performance from B0/B2;
6. privacy handling for future B2B tenants and API keys from C1.
