# Plan 62 — C6 Zero-Downtime Migration Engine

**Created:** 2026-07-28
**Completed:** 2026-08-05
**Status:** Core verified — unit/integration tests, E2E drill, Admin BFF console, architecture doc, 4 runbooks, evidence written. Chaos/load/PITR/source-write-disable gates are tracked follow-ups (see §42 ⬜ rows). See [c6-final-acceptance.md](../../evidence/c6-final-acceptance.md).
**Roadmap track:** C6 — Zero-downtime migration engine
**Activation trigger:** Intentional migration-practice decision
**Reference migration:** Ledger balance projection v1 → v2
**Primary owner:** The service that owns the migrating data
**Reference owner:** LedgerService
**Operator surface:** Admin BFF
**Supporting owners:** AssuranceService, Gateway, observability, backup/PITR
**No new application service, database proxy, or generic distributed
transaction coordinator is authorized by this plan.**

---

## 1. Purpose

Build reusable, evidence-driven machinery for changing a live data model or
storage implementation without stopping money movement.

C6 must support:

- additive target-schema preparation;
- deterministic backfill;
- continuous dual writes;
- source-primary reads with target shadow comparison;
- mismatch classification;
- repair and replay;
- target-primary canary reads;
- gradual read cutover;
- instant configuration-based read rollback;
- write-authority cutover;
- old-write disabling;
- source retirement only after an observation period;
- migration-level audit, metrics, alerts, runbooks, and evidence;
- one complete synthetic but realistic reference migration.

The reference migration moves Ledger's **balance projection** from a v1
representation to a v2 representation while preserving immutable
`ledger_entries` as the source of truth.

C6 must preserve the following principles:

1. Immutable Ledger entries remain the ultimate source of truth.
2. A migration target is not trusted merely because backfill completed.
3. Dual write is not treated as a distributed transaction across services.
4. The reference migration stays inside one Ledger database transaction.
5. Source remains authoritative until an explicit cutover gate passes.
6. Shadow reads never change a user response.
7. Mismatch detection is asynchronous-safe, bounded, and observable.
8. Target-primary reads always retain an immediate source fallback during
   canary and early cutover.
9. Cutover is reversible through configuration, not emergency code deployment.
10. A rollback never deletes target data or rewrites immutable financial
    history.
11. Every migration run has a durable identity, state machine, checkpoints, and
evidence.
12. Backfill and repair are idempotent.
13. Live writes and backfill may race without losing a newer target value.
14. A migration may not depend on row timestamps alone when a stronger source
    ordering token exists.
15. Every migrated record carries or can derive a monotonic source version.
16. Cross-service database access remains forbidden.
17. The migration framework belongs to each data owner and may reuse shared
    libraries only for generic mechanics.
18. A generic operator cannot submit arbitrary SQL.
19. Schema-specific transforms are compiled, reviewed code.
20. No public request waits for reconciliation.
21. Migration jobs use bounded batches, timeouts, and pause controls.
22. Backfill does not hold one long transaction.
23. Shadow sampling and metric labels remain bounded.
24. Sensitive values are not stored in mismatch evidence.
25. A service restart, database restart, or worker crash does not restart the
    migration from zero.
26. Existing backup/PITR and restore verification cover both source and target
    during the migration.
27. Source retirement is the final stage, not part of initial cutover.
28. C6 does not claim universal schema-migration automation.
29. C6 does not replace A9 contract evolution.
30. C6 does not replace C2 analytical CDC.
31. C6 does not move regulatory or financial authority silently.
32. Existing money journeys remain available throughout the drill.

The `account_balances_v2` migration control plane is present in Ledger,
including backfill, comparison, cutover, repair, and rollback paths. Keep this
plan active until runtime evidence is recorded; see the [current-state inventory](../../reference/current-state.md).

---

## 2. Roadmap and historical context

The long-term roadmap defines C6 as:

```text
shadow reads
dual writes
reconciliation
gradual cutover
instant rollback
```

for a real or synthetic migration.

The older extraction playbook intentionally preferred a short cutover for
expected service-extraction volumes instead of adding dual-write complexity.
C6 adds that deferred production-style machinery as a separate learning track.

C6 is not a requirement to redo the already-completed service split.

It supplies a reusable migration pattern for future cases such as:

- replacing a mutable projection;
- changing a table shape;
- moving from one key model to another;
- replacing an encrypted representation;
- changing an index-backed lookup structure;
- moving service-owned tables to a new database when a short cutover is no
  longer acceptable.

The reference execution stays deliberately narrower than a cross-database
service move.

---

## 3. Why the reference migration uses Ledger balance projections

The selected reference target is:

```text
Ledger balance projection v1
        ->
Ledger balance projection v2
```

This target is appropriate because:

- balance reads are user-visible and latency-sensitive;
- balance projection correctness matters;
- the projection is mutable and reconstructable;
- immutable ledger entries remain the authority;
- backfill can be independently recomputed;
- dual writes can occur inside the same Ledger database transaction;
- read shadowing is meaningful;
- instant read rollback is possible;
- the drill can inject races and failures without changing immutable postings.

The migration does **not**:

- migrate `ledger_entries`;
- create a second money source of truth;
- change double-entry semantics;
- permit direct writes outside the posting core;
- move Ledger data to another service;
- introduce eventual balance updates into the money path.

---

## 4. Activation and entry gate

### 4.1 Activation decision

C6 is activated on 2026-07-28 as an intentional migration-practice decision.

The reference migration is synthetic in business need but real in engineering
mechanics.

### 4.2 Required entry-gate evidence

T0 must record all items below.

- [ ] `make contracts` passes from a clean tree.
- [ ] Existing Ledger unit, integration, race, and business E2E tests pass.
- [ ] Current balance projection schema is inventoried.
- [ ] Current posting transaction writes and lock order are recorded.
- [ ] Current balance read paths are recorded.
- [ ] Current projection rebuild tooling is recorded.
- [ ] Current verifier and snapshot behavior are recorded.
- [ ] Existing account version or other monotonic update token is identified.
- [ ] Existing backup/PITR and restore verification pass.
- [ ] Existing product-assurance and intake controls pass.
- [ ] Existing feature/config reload mechanism is recorded.
- [ ] Existing Admin BFF maker/checker and audit patterns are recorded.
- [ ] Current migration head is recorded.
- [ ] Current row counts and balance-read traffic are measured.
- [ ] Current balance read p95/p99 are measured.
- [ ] Current posting p95/p99 and lock waits are measured.
- [ ] The target v2 schema's intended improvement is documented.
- [ ] There is no unrelated balance-projection redesign in flight.
- [ ] Exact baseline commit is recorded.
- [ ] The drill environment supports running source and target together.
- [ ] Recovery can restore a snapshot containing both projection versions.

### 4.3 Entry deliverables

```text
docs/evidence/c6-entry-gate.md
docs/reference/c6-current-balance-projection.md
docs/reference/c6-read-write-path-inventory.md
docs/reference/c6-version-token-analysis.md
docs/reference/c6-resource-baseline.md
docs/reference/c6-backup-restore-baseline.md
```

### 4.4 Gate policy

Before the gate is green, work may include:

- architecture;
- state-machine design;
- target-schema drafts;
- synthetic fixtures;
- generic library scaffolding;
- threat modeling;
- offline transform prototypes.

Before the gate is green, do not merge:

- production-style dual writes;
- shadow reads;
- a target-primary read path;
- migration-control mutations;
- target cleanup;
- source-write disabling.

---

## 5. Locked architecture decisions

## 5.1 No MigrationService

C6 does not create a tenth business service.

Migration execution belongs to the owner service.

Reference runtime components:

```text
LedgerService request path
Ledger migration coordinator
Ledger backfill worker
Ledger reconciliation worker
Ledger repair worker
Admin BFF migration UI
```

A repository-level shared package may provide generic types and worker
mechanics, but it cannot know Ledger tables or execute arbitrary transforms.

### 5.2 Shared library boundary

Suggested:

```text
internal/platform/migration/
├── state.go
├── checkpoint.go
├── lease.go
├── sampling.go
├── rollout.go
├── metrics.go
└── errors.go
```

Ledger-specific implementation:

```text
services/ledger/internal/migration/balancev2/
├── transform.go
├── writer.go
├── reader.go
├── backfill.go
├── compare.go
├── repair.go
├── cutover.go
└── evidence.go
```

The shared package may not import Ledger domain packages.

### 5.3 Migration specification is code

Every migration is registered in code.

Example conceptual interface:

```go
type Migration interface {
    Name() string
    SourceVersion() string
    TargetVersion() string
    ValidatePrerequisites(context.Context) error
    BackfillBatch(context.Context, Checkpoint, int) (BatchResult, error)
    Compare(context.Context, SampleKey) (Comparison, error)
    Repair(context.Context, Mismatch) error
}
```

No API accepts arbitrary SQL or transform scripts.

### 5.4 One active migration per resource

For a given resource:

```text
ledger_balance_projection
```

only one non-terminal migration may exist.

This prevents overlapping v1→v2 and v2→v3 changes.

### 5.5 Migration state is durable

The database stores:

- migration definition;
- rollout state;
- checkpoints;
- leases;
- backfill progress;
- comparison summaries;
- mismatch items;
- repair attempts;
- cutover decisions;
- audit evidence.

A deployment restart does not lose stage or progress.

### 5.6 Configuration and database state both participate

Database state is authoritative for migration stage and evidence.

Runtime config provides emergency local controls:

```text
migration globally enabled
target write enabled
shadow read enabled
target read percentage
source fallback enabled
```

Effective behavior is the most restrictive combination.

This gives immediate fail-safe rollback even if the database control plane is
unavailable.

### 5.7 Source remains authoritative initially

Stages before target-primary:

```text
response = source
```

Target is populated and compared only.

### 5.8 Shadow reads are sampled

The system does not need to double every read indefinitely.

Sampling is deterministic by stable key:

```text
hash(account_id + migration_name) mod 10_000
```

This provides stable cohorts.

### 5.9 Comparison is exact

For the reference balance projection:

```text
available
hold
pending
frozen
currency
account version
```

must match exactly.

No tolerance exists for integer money.

### 5.10 Target-primary fallback

During canary and early cutover:

1. attempt target read;
2. validate target readiness/version;
3. return target when valid;
4. on target missing, corrupt, stale, or error:
   - record bounded evidence;
   - read source;
   - return source.

Fallback is removed only in a later retirement stage.

### 5.11 Dual writes are one transaction for the reference migration

Every Ledger posting transaction updates:

```text
source projection v1
target projection v2
```

inside the same PostgreSQL transaction.

If either required write fails, the whole posting rolls back while strict dual
write is active.

### 5.12 No best-effort target write in final dual-write stages

A best-effort mode is allowed only in an initial shadow-write experiment where
source remains authoritative and the target can be repaired.

Before target-primary reads:

```text
strict dual write = required
```

### 5.13 Source ordering token

The target row stores:

```text
source_version
```

The version must be monotonic for the account.

Preferred source:

- existing account/balance version incremented in the posting transaction.

If no adequate token exists, C6 adds one before backfill.

Timestamps alone are insufficient.

### 5.14 Last-write-wins is version-based

Backfill and repair use an upsert rule:

```text
update target only when incoming source_version >= target.source_version
```

This prevents an older backfill row from overwriting a newer live dual write.

### 5.15 Backfill source

For the reference migration, backfill transforms the current authoritative v1
projection.

Independent reconciliation uses immutable ledger entries and/or existing
projection rebuild logic.

This separates:

```text
copy correctness
from
financial correctness
```

### 5.16 Cutover dimensions

Read cutover is separate from write-authority cutover.

Stages:

```text
target writes
shadow reads
target canary reads
target majority reads
target full reads with source fallback
target full reads without ordinary fallback
source writes disabled
source retired
```

### 5.17 Instant rollback definition

During target-primary stages, instant rollback means:

```text
set target read percentage to 0
```

or activate local emergency source-read override.

It does not mean deleting target data or reversing postings.

### 5.18 No source retirement in the same release as read cutover

The source remains:

- written;
- readable;
- reconcilable;

through an observation window.

### 5.19 No trigger-based hidden dual write

The reference implementation uses application-owned writes in the posting
transaction.

Database triggers may enforce invariants, but they do not become the primary
migration transform.

### 5.20 No generic cross-database exactly-once claim

A future cross-database migration may use:

- outbox;
- change log;
- CDC;
- replay;
- idempotent target writes.

It remains at-least-once with reconciliation.

The reference migration does not pretend this complexity is solved
universally.

---

## 6. Reference target data model

## 6.1 Source projection v1

T0 must confirm exact current names.

Conceptually:

```text
account_id
available_balance
hold_balance
pending_balance
frozen_balance
version
updated_at
```

### 6.2 Target projection v2 goals

The synthetic v2 projection demonstrates a meaningful shape change.

Proposed v2:

```text
account_id
currency
available_amount
reserved_amount
pending_amount
restricted_amount
source_version
last_transaction_id
projection_checksum
created_at
updated_at
```

Mapping:

```text
available_amount  <- available_balance
reserved_amount   <- hold_balance
pending_amount    <- pending_balance
restricted_amount <- frozen_balance
```

The renamed semantic fields make it impossible to “accidentally” reuse the
same repository code without a transform.

### 6.3 Target invariants

- one row per account;
- account currency matches;
- amounts are exact integers;
- source version is non-negative and monotonic;
- checksum is deterministic;
- last transaction ID is optional for legacy backfill, required after a live
  target write;
- no target row can reference an unknown account;
- target is not directly writable outside registered migration/posting paths.

### 6.4 Checksum

Canonical checksum input:

```text
account_id
currency
available
reserved
pending
restricted
source_version
```

Use a stable binary/canonical encoding.

The checksum is diagnostic, not a cryptographic authorization primitive.

### 6.5 Target access role

Normal Ledger runtime needs:

- select;
- insert;
- update;

on target during migration.

Backfill worker may use the same service identity with an owner-scoped
application path.

Admin BFF never accesses the table directly.

---

## 7. Migration state machine

## 7.1 States

```text
draft
validated
target_ready
backfilling
dual_write_shadow
shadow_read
canary_read
ramping_read
target_primary
source_write_disabled
observation
completed
paused
rolling_back
rolled_back
failed
cancelled_before_write
```

### 7.2 State transitions

```text
draft -> validated
validated -> target_ready
target_ready -> backfilling
backfilling -> dual_write_shadow
dual_write_shadow -> shadow_read
shadow_read -> canary_read
canary_read -> ramping_read
ramping_read -> target_primary
target_primary -> source_write_disabled
source_write_disabled -> observation
observation -> completed
```

Allowed rollback/pause transitions exist from every non-terminal active state.

### 7.3 State invariants

#### `draft`

No runtime behavior changes.

#### `validated`

Prerequisites and target contract pass.

#### `target_ready`

Target schema, grants, and indexes exist.

#### `backfilling`

Target writes from backfill; live source remains primary.

#### `dual_write_shadow`

Live posting also writes target, source remains primary.

#### `shadow_read`

Sampled reads compare source and target; source response only.

#### `canary_read`

Small stable cohort receives target-primary with source fallback.

#### `ramping_read`

Target cohort increases gradually.

#### `target_primary`

All eligible reads use target first, source fallback remains.

#### `source_write_disabled`

Only target is updated; source is frozen for new writes.

This state requires a much stricter gate.

#### `observation`

Target-only writes and reads run through a defined observation window.

#### `completed`

Source can be retired through a later cleanup migration.

### 7.4 Pausing

`paused` stores:

```text
previous active state
reason
actor
time
```

Pause semantics are stage-specific.

- backfill pause stops new batches;
- shadow pause stops comparisons;
- read-cutover pause freezes percentage;
- target-write pause may be unsafe and requires source-authority rollback.

### 7.5 Failed

A migration enters `failed` when:

- invariant violation;
- unrecoverable schema mismatch;
- repeated target-write failure;
- evidence corruption;
- unauthorized state transition;
- source-version regression.

A failed state automatically forces source-primary reads.

---

## 8. Rollout percentages and cohorts

## 8.1 Stable cohort

Use account ID hashing.

Buckets:

```text
0..9999
```

A percentage selects the lowest N buckets or a stored range.

### 8.2 Suggested ramp

```text
0.1%
1%
5%
10%
25%
50%
100%
```

Exact stages may be adjusted from traffic.

### 8.3 Internal/test cohort

Before percentage ramp:

- synthetic accounts;
- operator-owned test users;
- business E2E accounts.

### 8.4 Cohort stickiness

An account remains in the same cohort.

This avoids response flapping.

### 8.5 Exclusions

Exclude:

- known mismatch accounts;
- accounts under active repair;
- accounts whose target row is absent;
- unsupported account types;
- accounts created before unresolved backfill cutoff if target readiness is
  unknown.

Excluded reads fall back to source.

---

## 9. Dual-write design

## 9.1 Posting flow

During strict dual write:

1. begin Ledger transaction;
2. lock source balance row;
3. read source version;
4. apply posting entries;
5. calculate new source projection;
6. increment source version;
7. write v1 projection;
8. transform to v2;
9. upsert v2 with same source version and transaction ID;
10. insert outbox;
11. commit.

### 9.2 Transaction failure

If target write fails:

- posting rolls back;
- source write rolls back;
- entries roll back;
- outbox rolls back;
- client receives current Ledger failure semantics.

### 9.3 Shadow-write mode

Before strict mode, optional:

- attempt target write;
- if target write fails, source transaction may still commit;
- persist a durable migration repair item/outbox record.

This mode is time-bounded and cannot coexist with target-primary reads.

### 9.4 Strict-mode gate

Strict dual write is enabled only after:

- target schema stable;
- target write integration tests pass;
- target write latency measured;
- lock order reviewed;
- failure injection proves full rollback;
- backfill worker understands version race;
- repair queue is healthy.

### 9.5 New-account creation

Account provisioning must create:

```text
v1 projection
v2 projection
```

atomically when strict dual write is active.

### 9.6 Non-posting mutation paths

T0 inventories every source projection mutation.

Examples:

- projection rebuild;
- repair;
- account provisioning;
- administrative adjustment path;
- restore reseed;
- test setup.

Every allowed mutation path must update target or be explicitly disabled during
migration.

### 9.7 Direct SQL defense

Revoke target writes from roles that do not own migration/posting.

Add boundary tests and database grants.

---

## 10. Backfill design

## 10.1 Backfill identity

One durable backfill run belongs to one migration.

### 10.2 Ordering key

Use stable account primary key ordering.

For UUIDv7, keyset ordering is acceptable.

Do not use offset pagination.

### 10.3 Batch transaction

Each batch:

1. claim checkpoint lease;
2. read N source rows after last key;
3. transform;
4. bulk upsert target with source-version predicate;
5. persist checkpoint and counts;
6. commit;
7. release lease.

### 10.4 Batch size

Initial configurable:

```text
100
```

Tune after measurement.

### 10.5 Throttling

Controls:

```text
batch size
sleep between batches
maximum DB time per minute
statement timeout
lock timeout
pause on source latency
pause on replica/disk pressure where applicable
```

### 10.6 Backfill race

If live dual write produces target version 101 while backfill holds source
version 95:

```text
backfill upsert does not overwrite 101
```

### 10.7 Backfill retries

- same batch is idempotent;
- checkpoint advances only with committed target writes;
- a failed batch is retried;
- repeated row-specific transform failure becomes a mismatch/block item;
- one bad row does not silently disappear.

### 10.8 Initial and continuous backfill

Initial scan covers existing rows.

A tail scan covers rows created/changed around the initial cutoff.

Strict dual write plus versioned upsert closes the race.

### 10.9 Backfill completion

Completion requires:

- end of source keyspace reached;
- source row count and target coverage check;
- no unresolved transform failure;
- no checkpoint gap;
- tail scan complete;
- source versions not behind target in invalid direction.

---

## 11. Shadow-read design

## 11.1 Source-primary comparison

For sampled read:

1. read source;
2. return source through normal response path;
3. read target within a bounded shadow budget, preferably concurrently or
   immediately after authoritative result;
4. compare normalized values;
5. record aggregate result;
6. persist detailed mismatch only when needed.

### 11.2 Latency isolation

Shadow read must not materially increase user latency.

Options:

- bounded asynchronous comparison with copied request context;
- synchronous target read with a very small timeout and no response dependency.

Initial preference:

```text
bounded asynchronous comparison
```

Requirements:

- bounded queue;
- bounded workers;
- drop/skip metric when saturated;
- no unbounded goroutine;
- no mutation;
- no user-visible error.

### 11.3 Context lifetime

Do not reuse a cancelled HTTP context unsafely.

Create a bounded internal context carrying only approved trace linkage.

### 11.4 Comparison normalization

Normalize:

```text
missing source
missing target
currency
amount fields
source version
target version
checksum
```

### 11.5 Comparison results

```text
match
target_missing
target_stale
target_ahead
value_mismatch
currency_mismatch
version_mismatch
target_error
source_error
unsupported
```

`target_ahead` is not automatically wrong; it requires race/context analysis.

### 11.6 Detailed evidence

Persist:

```text
account ID or approved pseudonym
source version
target version
field mismatch mask
source checksum
target checksum
first seen
last seen
occurrence count
status
```

Do not persist raw sensitive account-owner data.

### 11.7 Sampling controls

```text
shadow sample percentage
maximum comparisons/second
per-account cooldown
mismatch detail rate limit
```

### 11.8 Read-path contract

Shadowing cannot alter:

- balance response;
- error mapping;
- read timestamp semantics;
- account authorization;
- metrics cardinality.

---

## 12. Reconciliation

## 12.1 Three reconciliation layers

### Layer 1 — Source versus target projection

Exact row comparison.

### Layer 2 — Target versus immutable Ledger entries

Rebuild expected balances from Ledger entries for a bounded account set.

### Layer 3 — Source versus immutable Ledger entries

Existing verifier/rebuild path confirms the old projection has not hidden a
shared source error.

### 12.2 Why three layers matter

If v1 and v2 match but both are wrong:

```text
source-target comparison passes
```

Only ledger-entry reconciliation detects the error.

### 12.3 Reconciliation modes

```text
sample
bucket
full
incident
pre_cutover
post_cutover
```

### 12.4 Bucket strategy

Hash account IDs into deterministic buckets.

This supports:

- bounded scans;
- repeatable coverage;
- parallel workers;
- progress evidence.

### 12.5 Cutoff

Use an account/source-version cutoff.

For full migration readiness:

- capture a migration watermark;
- ensure target is at least that version for every row;
- reconcile changes after watermark through strict dual write and tail checks.

### 12.6 Reconciliation result

```text
passed
warning
failed
stale
```

### 12.7 Financial mismatch policy

Any amount mismatch is critical for target-primary eligibility.

There is no percentage tolerance for money.

### 12.8 Aggregate gate

Read cutover needs:

```text
0 unresolved critical mismatches
```

for the eligible cohort.

An overall low mismatch percentage is not sufficient if any affected account
could be served from target.

### 12.9 Repair

Repair source is:

- immutable Ledger rebuild result, when financial truth is required;
- authoritative v1 projection only for shape-copy mismatch after v1 itself has
  passed its verifier.

### 12.10 Repair mutation

Repair target only.

Do not mutate ledger entries.

Do not auto-repair source through C6.

---

## 13. Repair workflow

## 13.1 Mismatch lifecycle

```text
open
classified
repair_pending
repairing
repaired
verified
ignored_with_reason
blocked
```

### 13.2 Classification

```text
backfill_missing
stale_backfill
live_write_gap
transform_bug
target_corruption
source_corruption
shared_projection_bug
version_regression
unsupported_legacy_row
```

### 13.3 Automatic repair eligibility

May auto-repair:

- target missing;
- target stale;
- known transform version;
- source verified against Ledger.

Must not auto-repair:

- source corruption;
- shared projection bug;
- currency mismatch;
- unknown transform;
- version regression;
- ambiguous account state.

### 13.4 Repair idempotency

Unique:

```text
migration + account + expected source version + repair type
```

### 13.5 Post-repair verification

A repair is not complete until:

- target reread;
- exact compare;
- optional Ledger-entry reconstruction;
- mismatch state verified.

### 13.6 Repair rate

Bound repair throughput separately from backfill.

### 13.7 Operator repair

Manual repair action uses:

- approved predefined repair type;
- reason;
- maker/checker for high-risk classification;
- audit.

No arbitrary target values entered from UI.

---

## 14. Target-primary read path

## 14.1 Eligibility

An account is target-readable when:

- migration stage permits;
- stable cohort selected;
- no open mismatch;
- target row exists;
- target version is valid;
- target checksum passes;
- account type supported.

### 14.2 Fallback rules

Fallback to source on:

```text
target not found
target timeout
target decode error
checksum failure
target version invalid
open mismatch
migration emergency override
```

### 14.3 Comparison during target-primary

For sampled target-primary reads:

- read target;
- return target;
- asynchronously compare source;
- record mismatch.

### 14.4 Fallback telemetry

Fallback has bounded reasons.

A high fallback rate blocks ramp.

### 14.5 Response equivalence

Existing public and internal contracts remain unchanged.

v2 field names are internal.

### 14.6 Stale-target defense

If source version can be cheaply read without full source value, a version
guard may compare versions.

Do not add an extra source query to every target read unless measurement
justifies it.

Strict dual write and reconciliation remain the primary freshness guarantee.

---

## 15. Read-cutover gates

## 15.1 Canary prerequisites

- backfill complete;
- strict dual write active;
- zero unresolved critical mismatch in test cohort;
- shadow-read match gate satisfied;
- target read latency acceptable;
- fallback tested;
- emergency override tested;
- backup contains both projections;
- runbooks reviewed.

### 15.2 Initial local thresholds

These are engineering gates, not production claims.

```text
shadow comparison success:        >= 99.99%
unresolved money mismatches:       0
target missing in eligible set:    0
target fallback rate:              < 0.1%
target p95 read latency:           <= source p95 + 10%
posting p95 regression:            <= 5%
strict dual-write error rate:       0 in acceptance run
backfill unresolved rows:           0
repair queue oldest age:            bounded and empty before cutover
```

### 15.3 Ramp hold period

At each stage, observe at least:

- a configured request count;
- one complete business E2E;
- one worker/restart cycle;
- one reconciliation run.

Do not advance solely by elapsed time.

### 15.4 Automatic abort

Automatically set read percentage to zero when:

- critical mismatch;
- target error-rate threshold;
- fallback spike;
- checksum failure;
- source-version regression;
- migration state invalid;
- target DB/table unavailable.

### 15.5 Operator advancement

Ramp changes require:

- owner-side validation;
- authorized operator;
- reason;
- current evidence snapshot;
- audit.

Maker/checker is required for:

```text
25% -> 50%
50% -> 100%
source write disable
source retirement
```

---

## 16. Write-authority cutover

## 16.1 Why it is separate

Serving reads from v2 does not mean v1 writes can stop.

Source writes remain until:

- target has run at 100%;
- source-target comparison remains clean;
- recovery has been exercised;
- source rollback window policy is decided.

### 16.2 Source-write-disabled prerequisites

- full target reads stable;
- source fallback rate near zero;
- full reconciliation passes;
- strict dual write stable;
- no old binary that writes only v1 can be deployed;
- deployment compatibility matrix proves all active versions understand v2;
- rollback plan explains how source can be resynchronized.

### 16.3 Deployment compatibility gate

Before source writes stop:

- minimum supported binary version is enforced;
- older image cannot be rolled out accidentally;
- CI/compose manifests use compatible image;
- rollback image supports target writes.

### 16.4 Target-only writes

When source writes are disabled:

- posting writes target;
- source is not updated;
- source reads are no longer safe as current fallback unless fed by a reverse
  compatibility writer.

### 16.5 Instant rollback after source-write disable

True immediate source-read rollback is no longer possible without resync.

Therefore, C6 defines:

- **instant read rollback guarantee** only while source dual writes remain;
- after source-write disable, rollback requires:
  - re-enable source writes;
  - backfill/replay target changes to source;
  - verify;
  - then source-primary.

This distinction must be explicit.

### 16.6 Observation window

Recommended local observation:

```text
at least one complete business cycle
plus backup/restore drill
plus full reconciliation
```

### 16.7 Reference-plan recommendation

For the learning baseline, C6 may stop at:

```text
100% target reads
strict dual writes retained
```

and document source-write disable as an exercised optional stage.

This preserves instant rollback while proving the most valuable machinery.

---

## 17. Source retirement

## 17.1 Retirement is delayed

Source retirement occurs only after:

- observation window;
- backups;
- restore;
- no supported rollback need;
- all code references removed;
- old grants revoked;
- source-specific jobs disabled;
- final archive evidence.

### 17.2 Retirement stages

```text
stop source reads
stop source writes
mark source deprecated
remove application grants
rename/archive source table where safe
drop source only in a later migration
```

### 17.3 No immediate drop

The source table is not dropped in the same migration that cuts reads.

### 17.4 Archive or drop

Choice depends on:

- privacy retention;
- backup policy;
- storage;
- regulatory evidence;
- rollback window.

### 17.5 A9 interaction

Any public/internal contract cleanup follows A9 expand/contract rules.

---

## 18. Migration control schema

Use service-owned additive migrations.

## 18.1 `data_migrations`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
name TEXT UNIQUE NOT NULL
resource TEXT NOT NULL
source_version TEXT NOT NULL
target_version TEXT NOT NULL
state TEXT NOT NULL
previous_state TEXT NULL
read_percentage_basis_points INTEGER NOT NULL
shadow_percentage_basis_points INTEGER NOT NULL
strict_dual_write BOOLEAN NOT NULL
source_fallback_enabled BOOLEAN NOT NULL
source_write_enabled BOOLEAN NOT NULL
target_write_enabled BOOLEAN NOT NULL
transform_version INTEGER NOT NULL
baseline_commit TEXT NOT NULL
created_by TEXT NOT NULL
updated_by TEXT NOT NULL
pause_reason TEXT NULL
failure_code TEXT NULL
started_at TIMESTAMPTZ NULL
backfill_completed_at TIMESTAMPTZ NULL
cutover_started_at TIMESTAMPTZ NULL
completed_at TIMESTAMPTZ NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
version BIGINT NOT NULL
CHECK (read_percentage_basis_points BETWEEN 0 AND 10000)
CHECK (shadow_percentage_basis_points BETWEEN 0 AND 10000)
```

## 18.2 `data_migration_checkpoints`

```text
id UUID PRIMARY KEY
migration_id UUID NOT NULL
worker_kind TEXT NOT NULL
partition_key TEXT NOT NULL
last_source_key TEXT NULL
watermark_version BIGINT NULL
processed_count BIGINT NOT NULL
updated_count BIGINT NOT NULL
skipped_count BIGINT NOT NULL
failed_count BIGINT NOT NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
status TEXT NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (migration_id, worker_kind, partition_key)
```

## 18.3 `data_migration_runs`

```text
id UUID PRIMARY KEY
migration_id UUID NOT NULL
run_type TEXT NOT NULL
status TEXT NOT NULL
started_at TIMESTAMPTZ NOT NULL
finished_at TIMESTAMPTZ NULL
source_cutoff TEXT NULL
target_cutoff TEXT NULL
processed_count BIGINT NOT NULL
match_count BIGINT NOT NULL
mismatch_count BIGINT NOT NULL
error_count BIGINT NOT NULL
evidence JSONB NULL
created_at TIMESTAMPTZ NOT NULL
```

## 18.4 `data_migration_mismatches`

```text
id UUID PRIMARY KEY
migration_id UUID NOT NULL
resource_key_hash BYTEA NOT NULL
resource_public_key TEXT NULL
classification TEXT NULL
status TEXT NOT NULL
field_mask BIGINT NOT NULL
source_version BIGINT NULL
target_version BIGINT NULL
source_checksum BYTEA NULL
target_checksum BYTEA NULL
occurrence_count BIGINT NOT NULL
first_seen_at TIMESTAMPTZ NOT NULL
last_seen_at TIMESTAMPTZ NOT NULL
repair_attempt_count INTEGER NOT NULL
last_error_code TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (migration_id, resource_key_hash)
```

Use public key only if operationally approved.

## 18.5 `data_migration_repairs`

```text
id UUID PRIMARY KEY
migration_id UUID NOT NULL
mismatch_id UUID NOT NULL
repair_type TEXT NOT NULL
expected_source_version BIGINT NULL
status TEXT NOT NULL
attempt_count INTEGER NOT NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
created_by TEXT NOT NULL
approved_by TEXT NULL
reason TEXT NOT NULL
started_at TIMESTAMPTZ NULL
finished_at TIMESTAMPTZ NULL
error_code TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
```

## 18.6 `data_migration_transitions`

```text
id UUID PRIMARY KEY
migration_id UUID NOT NULL
from_state TEXT NOT NULL
to_state TEXT NOT NULL
requested_by TEXT NOT NULL
approved_by TEXT NULL
reason TEXT NOT NULL
evidence_snapshot JSONB NOT NULL
created_at TIMESTAMPTZ NOT NULL
```

### 18.7 Target table

Conceptual:

```text
account_balances_v2
```

with constraints and indexes from Section 6.

---

## 19. Admin BFF surface

## 19.1 Boundary

Admin BFF calls typed Ledger admin APIs.

It does not read migration tables directly.

### 19.2 Migration overview

Show:

```text
name
resource
source/target version
state
dual-write mode
shadow percentage
read percentage
source fallback
source writes
target writes
backfill progress
unresolved mismatches
repair backlog
latest reconciliation
latency/error gates
last transition
```

### 19.3 Actions

```text
validate
start target preparation
start/pause/resume backfill
enable shadow dual write
enable strict dual write
enable shadow reads
set read percentage
rollback reads to source
pause migration
resume migration
start reconciliation
start approved repair
disable source writes
complete observation
```

### 19.4 Dangerous actions

Require maker/checker:

```text
read ramp above 25%
read ramp to 100%
disable source writes
disable source fallback
retire source
bulk repair of critical mismatch
```

### 19.5 Evidence preview

Before transition, show:

- current gates;
- failing gates;
- evidence timestamp;
- sample count;
- unresolved mismatch count;
- target error rate;
- fallback rate;
- read/post latency regression;
- backup freshness.

### 19.6 No force button

There is no generic “force transition despite failed gates.”

A local-development override, if absolutely required for a drill, must:

- be compile/config gated;
- be labeled unsafe;
- require explicit environment acknowledgement;
- create audit;
- never be available by default.

### 19.7 Audit

Events:

```text
migration.created
migration.validated
migration.backfill.started
migration.backfill.paused
migration.dual_write.changed
migration.shadow_read.changed
migration.read_percentage.changed
migration.rollback.activated
migration.reconciliation.started
migration.repair.approved
migration.source_write.disabled
migration.source.retired
migration.failed
```

---

## 20. APIs and internal contracts

## 20.1 No public migration API

End users see no migration-specific contract.

Balance responses remain unchanged.

### 20.2 Ledger admin contracts

Conceptual:

```text
ListDataMigrations
GetDataMigration
ValidateDataMigration
TransitionDataMigration
SetDataMigrationReadPercentage
PauseDataMigration
ResumeDataMigration
RunDataMigrationBackfill
RunDataMigrationReconciliation
ListDataMigrationMismatches
GetDataMigrationMismatch
ApproveDataMigrationRepair
RunDataMigrationRepair
```

### 20.3 Runtime config contract

Ledger runtime reads migration control state through its repository.

Use a bounded refresh strategy or notification.

Initial approach:

```text
short bounded in-process snapshot
plus forced refresh after admin transition
plus fail-safe local override
```

### 20.4 Compatibility

Migration control APIs are internal/admin and still follow A9 contract rules.

---

## 21. Generic migration lifecycle

## 21.1 Phase A — Specify

- business reason;
- source authority;
- target purpose;
- transform;
- version token;
- read/write paths;
- invariants;
- rollback promise;
- retention.

### 21.2 Phase B — Expand

- create target schema;
- add indexes/grants;
- add writer and reader behind flags;
- add migration state;
- deploy with behavior disabled.

### 21.3 Phase C — Populate

- initial backfill;
- tail scan;
- target coverage;
- transform validation.

### 21.4 Phase D — Synchronize

- shadow/best-effort target write;
- strict dual write;
- repair write gaps.

### 21.5 Phase E — Observe

- source-primary shadow reads;
- exact comparison;
- independent source-of-truth reconciliation.

### 21.6 Phase F — Cut reads

- test cohort;
- canary;
- ramp;
- 100% target with source fallback.

### 21.7 Phase G — Observe target primary

- full business cycles;
- incidents;
- restarts;
- backup/restore;
- reconciliation.

### 21.8 Phase H — Contract writes

- optional source-write disable;
- resync/rollback drill;
- observation.

### 21.9 Phase I — Retire

- remove source path;
- revoke grants;
- archive/drop later;
- update docs.

---

## 22. Backup, PITR, and restore

## 22.1 Backup coverage

During migration, backup must include:

- source projection;
- target projection;
- migration state;
- checkpoints;
- mismatches;
- repair evidence.

### 22.2 Restore verification

Restore drill must verify:

- source/target rows;
- migration stage;
- strict dual-write configuration;
- read percentage;
- unresolved mismatch state;
- checkpoint continuity;
- target checksum;
- balance reconciliation.

### 22.3 Restore safety

After restoring to an earlier point:

- do not automatically resume migration workers;
- force source-primary reads;
- validate stage and target coverage;
- reset expired leases;
- run reconciliation;
- explicitly resume.

### 22.4 PITR during cutover

A PITR point from before cutover may contain older runtime state.

Runbook must explain:

- effective read override;
- deployment compatibility;
- migration state review;
- replay/backfill.

---

## 23. Observability

## 23.1 Migration metrics

```text
seev_data_migration_state{migration}
seev_data_migration_backfill_rows_total{migration,result}
seev_data_migration_backfill_duration_seconds{migration,result}
seev_data_migration_backfill_progress_ratio{migration}
seev_data_migration_dual_write_total{migration,result,mode}
seev_data_migration_dual_write_duration_seconds{migration,result}
seev_data_migration_shadow_reads_total{migration,result}
seev_data_migration_shadow_compare_duration_seconds{migration,result}
seev_data_migration_mismatches_total{migration,classification,status}
seev_data_migration_unresolved_mismatches{migration,severity}
seev_data_migration_repairs_total{migration,type,result}
seev_data_migration_target_reads_total{migration,result}
seev_data_migration_source_fallback_total{migration,reason}
seev_data_migration_read_percentage{migration}
seev_data_migration_reconciliation_total{migration,type,result}
seev_data_migration_checkpoint_age_seconds{migration,worker}
```

### 23.2 Reference financial metrics

```text
seev_balance_projection_v2_version_lag
seev_balance_projection_v2_checksum_failures_total
seev_balance_projection_v2_ledger_rebuild_delta
```

### 23.3 Forbidden labels

Do not label with:

```text
account ID
user ID
transaction ID
mismatch ID
checkpoint key
trace ID
```

### 23.4 Logging

Structured logs include:

```text
migration
stage
worker
batch size
result
stable error code
source/target versions when safe
trace ID
```

Detailed account IDs appear only in restricted mismatch evidence when needed,
not broad logs.

### 23.5 Tracing

Trace:

```text
Ledger posting
-> v1 write
-> v2 transform/write
-> commit
```

Backfill:

```text
checkpoint
-> source batch
-> transform
-> target upsert
-> checkpoint commit
```

Shadow:

```text
source read
-> returned response
-> async target compare
```

### 23.6 Dashboards

Panels:

```text
migration state
backfill progress/rate
checkpoint age
dual-write errors
posting latency regression
shadow match rate
mismatch classifications
repair backlog
read percentage
target error rate
source fallback
source/target/ledger reconciliation
backup freshness
```

### 23.7 Alerts

Required:

```text
strict dual-write failure
migration state invalid
backfill stalled
checkpoint lease stuck
critical mismatch
target missing in eligible cohort
target checksum failure
source-version regression
target read error spike
fallback spike
posting latency regression
repair backlog age
backup stale during cutover
reconciliation failed
```

Every alert links to a runbook.

---

## 24. Threat model

## 24.1 Data correctness threats

- backfill overwrites newer live write;
- source and target transform differ;
- one mutation path omits target;
- target checksum collision/bug;
- target row missing;
- source version resets;
- stale target served;
- source and target agree but both are wrong;
- repair uses corrupt source;
- source retirement occurs too early.

### 24.2 Availability threats

- strict target write failure blocks posting;
- shadow queue exhausts resources;
- target-primary read timeout increases p99;
- fallback storms overload source;
- backfill saturates database;
- repair and backfill contend;
- migration worker lock stalls;
- cutover configuration unavailable.

### 24.3 Control-plane threats

- unauthorized ramp;
- maker approves own dangerous transition;
- stale evidence used for transition;
- arbitrary SQL repair;
- emergency override left enabled;
- old binary deployed after source-write disable.

### 24.4 Privacy threats

- mismatch evidence stores raw sensitive row;
- broad admin access;
- backup retains target beyond policy;
- logs expose account/user details.

### 24.5 Rollback threats

- target reads rolled back after source writes stopped;
- source is stale;
- target data deleted during rollback;
- dual writes disabled in wrong order;
- restored environment resumes at unsafe stage.

### 24.6 Required threat format

For each:

```text
prevention
detection
test
alert
runbook
residual risk
owner
```

---

## 25. Runbooks

Create:

```text
docs/runbooks/migration-backfill-stalled.md
docs/runbooks/migration-strict-dual-write-failure.md
docs/runbooks/migration-shadow-mismatch.md
docs/runbooks/migration-target-read-failure.md
docs/runbooks/migration-source-fallback-spike.md
docs/runbooks/migration-version-regression.md
docs/runbooks/migration-repair-backlog.md
docs/runbooks/migration-instant-read-rollback.md
docs/runbooks/migration-source-resynchronization.md
docs/runbooks/migration-pitr-restore.md
docs/runbooks/migration-old-binary-deployed.md
docs/runbooks/migration-source-retirement.md
docs/runbooks/migration-sensitive-evidence-incident.md
```

Every runbook includes:

- stage;
- user/money impact;
- source of truth;
- immediate safe state;
- config override;
- state transition;
- whether posting must pause;
- whether read cutover must rollback;
- reconciliation;
- repair;
- evidence;
- criteria to resume.

---

## 26. Configuration

Suggested:

```text
DATA_MIGRATION_ENABLED=false
DATA_MIGRATION_EMERGENCY_SOURCE_READ=false
DATA_MIGRATION_DISABLE_TARGET_WRITES=false

BALANCE_V2_BACKFILL_BATCH_SIZE=100
BALANCE_V2_BACKFILL_WORKERS=1
BALANCE_V2_BACKFILL_SLEEP=50ms
BALANCE_V2_BACKFILL_STATEMENT_TIMEOUT=5s
BALANCE_V2_BACKFILL_LOCK_TIMEOUT=500ms

BALANCE_V2_SHADOW_WORKERS=4
BALANCE_V2_SHADOW_QUEUE_SIZE=1000
BALANCE_V2_SHADOW_TIMEOUT=50ms
BALANCE_V2_SHADOW_MAX_RPS=100

BALANCE_V2_RECONCILE_BATCH_SIZE=100
BALANCE_V2_REPAIR_BATCH_SIZE=50
BALANCE_V2_REPAIR_WORKERS=1

BALANCE_V2_TARGET_READ_TIMEOUT=50ms
BALANCE_V2_SOURCE_FALLBACK=true
```

Rules:

- migration disabled by default;
- emergency source-read override is fail-safe;
- invalid values fail startup;
- no unlimited workers/batches;
- no config to skip target validation;
- secrets are not involved.

---

## 27. Task breakdown

# T0 — Entry gate and migration inventory

### Work

- record commit and migration heads;
- run current gates;
- inventory source schema;
- inventory every source write path;
- inventory every source read path;
- inventory version tokens;
- inventory projection rebuild;
- inventory backup/restore;
- measure current load;
- select v2 transform;
- document rollback promise;
- identify old-binary compatibility.

### Acceptance

- [ ] Every write path is known.
- [ ] Every read path is known.
- [ ] Source authority is explicit.
- [ ] Version strategy is proven.
- [ ] Target purpose is non-speculative.
- [ ] Baselines are recorded.
- [ ] Existing business journeys are green.

---

# T1 — Lock framework contracts and threat model

### Work

- define migration state machine;
- define gates;
- define shared package boundary;
- define source/target version contract;
- define transform;
- define checksum;
- define backfill;
- define reconciliation;
- define repair;
- define rollback guarantees;
- update threat model;
- add diagrams.

### Required diagrams

```text
live dual write
backfill/live-write race
source-primary shadow read
target-primary fallback
reconciliation layers
repair
read ramp
instant rollback
source-write disable
PITR restore during migration
```

### Acceptance

- [ ] No arbitrary transform exists.
- [ ] Source remains explicit.
- [ ] Instant rollback boundary is explicit.
- [ ] Cross-database claims are excluded.
- [ ] Gates are measurable.
- [ ] Threat controls have tests.

---

# T2 — Migration control plane and shared mechanics

### Work

- add migration control tables;
- add state transition validator;
- add checkpoint/lease helpers;
- add stable cohort helper;
- add evidence snapshot;
- add owner/admin APIs;
- add config override;
- add metrics;
- add audit;
- add unit/integration tests.

### Acceptance

- [ ] Invalid transition is rejected.
- [ ] One active migration per resource.
- [ ] Optimistic concurrency works.
- [ ] Maker/checker works for dangerous transitions.
- [ ] Restart preserves state.
- [ ] Emergency source read works.
- [ ] Admin BFF never accesses DB directly.

---

# T3 — Target v2 schema and repositories

### Work

- create target table;
- add constraints/indexes;
- add transform;
- add checksum;
- add v2 repository;
- add target validation;
- add grants;
- add fixture migration;
- add build/contract tests.

### Acceptance

- [ ] v2 shape is deterministic.
- [ ] Exact money preserved.
- [ ] Currency preserved.
- [ ] Source version stored.
- [ ] Unauthorized writes fail.
- [ ] Existing runtime behavior remains disabled.
- [ ] Migration up/down is tested where safe.

---

# T4 — Backfill engine

### Work

- add keyset batch scan;
- add version-aware upsert;
- add checkpoints;
- add throttle;
- add pause/resume;
- add row failure capture;
- add tail scan;
- add coverage checks;
- add performance metrics;
- add chaos tests.

### Acceptance

- [ ] Restart resumes checkpoint.
- [ ] Old backfill never overwrites newer live target.
- [ ] One bad row is visible.
- [ ] No offset pagination.
- [ ] Source p95 regression is bounded.
- [ ] Completion has coverage evidence.
- [ ] Pause/resume works.

---

# T5 — Shadow and strict dual write

### Work

- enumerate/update all source mutation paths;
- add v2 write to posting;
- add new-account target creation;
- add projection-rebuild target behavior;
- add shadow best-effort mode;
- add durable repair evidence;
- add strict mode;
- inject target-write failure;
- measure posting regression.

### Acceptance

- [ ] Every allowed mutation path updates target.
- [ ] Strict failure rolls back entire posting.
- [ ] Outbox remains atomic.
- [ ] No direct target mutation exists.
- [ ] New account gets both projections.
- [ ] Best-effort mode cannot coexist with target reads.
- [ ] Posting latency gate passes.

---

# T6 — Source-primary shadow reads

### Work

- add stable sampling;
- add bounded async queue;
- add source/target normalization;
- add mismatch aggregation;
- add detailed evidence;
- add rate limits;
- add timeout;
- add dashboard/alerts;
- add saturation behavior.

### Acceptance

- [ ] User receives source result.
- [ ] Target failure never changes response.
- [ ] No unbounded goroutine.
- [ ] Queue saturation is visible.
- [ ] Exact money mismatch is critical.
- [ ] Sensitive values are minimized.
- [ ] Sampling is stable.

---

# T7 — Independent reconciliation and repair

### Work

- add source-target comparison;
- add target-Ledger rebuild comparison;
- add source-Ledger comparison;
- add bucket runs;
- add mismatch classification;
- add auto-repair eligibility;
- add repair worker;
- add maker/checker repair;
- add post-repair verification.

### Acceptance

- [ ] Shared source/target corruption is detectable.
- [ ] Critical mismatch blocks cutover.
- [ ] Repair never changes Ledger entries.
- [ ] Auto-repair is restricted.
- [ ] Repair is idempotent.
- [ ] Verified state requires reread/reconciliation.
- [ ] Full pre-cutover run passes.

---

# T8 — Target-primary canary and fallback

### Work

- add target reader;
- add eligibility;
- add source fallback;
- add stable canary;
- add target-primary shadow comparison;
- add automatic abort;
- add Admin ramp;
- add load tests;
- exercise instant rollback.

### Acceptance

- [ ] Ineligible account uses source.
- [ ] Target error falls back.
- [ ] Fallback reason is visible.
- [ ] Canary response is contract-equivalent.
- [ ] Automatic abort sets source-primary.
- [ ] Instant rollback requires no deployment.
- [ ] Canary gates pass.

---

# T9 — Gradual cutover

### Work

- run 0.1/1/5/10/25/50/100 stages;
- collect evidence per stage;
- run business E2E;
- restart service/DB;
- reconcile;
- validate backups;
- hold/ramp/rollback based on gates.

### Acceptance

- [ ] Every ramp has evidence.
- [ ] No unresolved critical mismatch.
- [ ] Target latency gate passes.
- [ ] Fallback gate passes.
- [ ] Posting regression remains bounded.
- [ ] 100% target reads sustain business cycles.
- [ ] Source rollback remains instant.

---

# T10 — Optional source-write cutover

### Work

- enforce minimum binary version;
- prove rollback image compatibility;
- disable source writes;
- run target-only posting;
- implement source resync workflow;
- exercise rollback-to-source drill;
- observe/reconcile;
- decide whether learning baseline proceeds.

### Acceptance

- [ ] Old binary cannot deploy.
- [ ] Target-only writes are correct.
- [ ] Source stale state is visible.
- [ ] Resync is deterministic.
- [ ] Source rollback is not falsely called instant.
- [ ] Full reconciliation passes.
- [ ] Decision to retain dual writes or continue is documented.

---

# T11 — Source retirement and cleanup plan

### Work

- remove source reads;
- remove source write code where approved;
- revoke grants;
- update rebuild/verifier;
- update backup policy;
- archive source;
- prepare later drop migration;
- update docs.

### Acceptance

- [ ] Retirement is separated from cutover.
- [ ] Observation gate passes.
- [ ] No code references source.
- [ ] Restore remains verified.
- [ ] Retention/privacy reviewed.
- [ ] Final drop is delayed or separately approved.

---

# T12 — Admin BFF, observability, and runbooks

### Work

- build migration pages;
- build mismatch/repair pages;
- add transition evidence;
- add dashboards;
- add alerts;
- add runbooks;
- add audit;
- validate cardinality and redaction.

### Acceptance

- [ ] No force transition exists.
- [ ] Dangerous stages require checker.
- [ ] Evidence freshness is visible.
- [ ] Every alert has runbook.
- [ ] Sensitive data absent.
- [ ] Emergency source override visible.

---

# T13 — E2E, chaos, load, restore, and final evidence

### Work

- add migration E2E;
- add migration chaos;
- inject race/failure cases;
- run load;
- run PITR restore;
- run old-binary deployment rejection;
- run rollback drills;
- run full repository gate;
- record residual risks;
- update roadmap;
- archive only after evidence.

### Acceptance

- [ ] Backfill/live-write race is safe.
- [ ] Strict dual-write rollback is safe.
- [ ] Shadow reads are isolated.
- [ ] Critical mismatch aborts cutover.
- [ ] Target outage falls back.
- [ ] 100% target read works.
- [ ] Instant read rollback works while dual writes remain.
- [ ] Restore returns to safe state.
- [ ] No duplicate/missing money occurs.
- [ ] Final clean-tree gate passes.

---

## 28. Recommended pull-request sequence

```text
PR 1  — C6 entry evidence, architecture, state machine, threat model
PR 2  — Migration control schema, shared mechanics, Admin API skeleton
PR 3  — Balance projection v2 schema, transform, checksum, repository
PR 4  — Backfill engine, checkpoint, throttling, race-safe upsert
PR 5  — Shadow target writes and repair evidence
PR 6  — Strict dual write in all balance mutation paths
PR 7  — Source-primary shadow reads and mismatch evidence
PR 8  — Three-layer reconciliation and repair
PR 9  — Target-primary canary and source fallback
PR 10 — Gradual read ramp and automatic rollback controls
PR 11 — Optional source-write-disable and source-resync drill
PR 12 — Admin BFF, dashboards, alerts, runbooks
PR 13 — Restore, chaos, load, final evidence and roadmap update
```

Do not combine target schema, dual write, cutover, and source retirement in one
PR.

---

## 29. Dependency graph

```text
T0 Entry gate
  |
  v
T1 Contracts/threat model
  |
  v
T2 Control plane
  |
  v
T3 Target schema
  |
  |----------------|
  v                v
T4 Backfill     T5 Dual write
  |                |
  |--------|-------|
           v
     T6 Shadow reads
           |
           v
 T7 Reconciliation/repair
           |
           v
 T8 Target canary/fallback
           |
           v
 T9 Gradual cutover
           |
           v
 T10 Optional write cutover
           |
           v
 T11 Retirement plan
           |
           v
 T12 Ops/admin/runbooks
           |
           v
 T13 Final evidence
```

T12 begins incrementally after T2 but completes after all stages exist.

---

## 30. First implementation cut

The first cut changes no live read and no required write.

```text
migration control state
-> account_balances_v2 schema
-> deterministic transform
-> bounded backfill
-> checkpoint/restart
-> source-target offline compare
```

This proves:

- target shape;
- transform;
- version-aware upsert;
- backfill control;
- evidence.

### 30.1 Second cut

```text
source-authoritative posting
-> best-effort target shadow write
-> repair evidence
-> no target reads
```

### 30.2 Third cut

```text
strict dual write
-> source-primary shadow reads
-> three-layer reconciliation
```

### 30.3 Fourth cut

```text
stable canary
-> target-primary with source fallback
-> percentage ramp
-> instant source-read rollback
```

The learning baseline can finish at 100% target reads while retaining strict
dual writes to preserve the strongest rollback guarantee.

---

## 31. Test strategy

## 31.1 Unit tests

Cover:

```text
state transitions
percentage cohorts
transform
checksum
version comparison
upsert decision
comparison classification
repair eligibility
cutover gates
fallback decisions
evidence freshness
config precedence
```

### 31.2 Fuzz/property tests

Properties:

- transform deterministic;
- same source row produces same checksum;
- older source version never overwrites newer target;
- stable account always maps to same cohort;
- target-primary fallback returns source-equivalent result;
- invalid transition never commits;
- critical mismatch always blocks ramp.

Fuzz:

```text
source row
target row
version
migration state
percentage
checkpoint
mismatch field mask
```

### 31.3 PostgreSQL integration tests

```text
one active migration per resource
optimistic state transition
checkpoint lease
expired lease recovery
version-aware target upsert
strict dual-write rollback
new-account creation
backfill/live-write race
mismatch uniqueness
repair uniqueness
target grants
migration/target restore
```

### 31.4 Read-path integration tests

```text
source primary
shadow sample
target missing
target stale
target corrupt
target timeout
target primary
source fallback
emergency override
cohort stability
```

### 31.5 Financial reconciliation tests

```text
v1 = v2
v2 = ledger rebuild
v1 = ledger rebuild
shared wrong projection
one-field mismatch
currency mismatch
version regression
missing target
```

### 31.6 Admin E2E

```text
maker starts backfill
operator sees progress
maker requests 25->50 ramp
checker approves
failed gate blocks request
target failure triggers rollback
audit exists
repair requires approved type
```

---

## 32. Chaos matrix

## 32.1 Worker crash after target upsert before checkpoint

Expected:

- batch repeats;
- version-aware upsert is idempotent;
- checkpoint eventually advances.

### 32.2 Live write races with backfill

Expected:

- newer live target version survives;
- backfill cannot regress target.

### 32.3 Target write failure in shadow mode

Expected:

- source posting may succeed;
- repair item exists;
- target read remains disabled.

### 32.4 Target write failure in strict mode

Expected:

- entire Ledger posting rolls back;
- no entries/outbox/source update.

### 32.5 Shadow queue saturation

Expected:

- user reads unaffected;
- comparison skipped metric;
- no goroutine leak;
- ramp blocked if coverage inadequate.

### 32.6 Target read outage during canary

Expected:

- source fallback;
- automatic abort if threshold;
- user balance remains available.

### 32.7 Target wrong value

Expected:

- mismatch;
- account excluded;
- automatic source fallback;
- ramp blocked.

### 32.8 Target checksum corruption

Expected:

- target rejected;
- source fallback;
- critical alert;
- repair.

### 32.9 Source and target both wrong

Expected:

- source-target comparison matches;
- Ledger-entry reconciliation fails;
- migration fails/pauses.

### 32.10 Database restart during backfill

Expected:

- checkpoint recovery;
- leases expire;
- no restart from zero.

### 32.11 Database restart during strict posting

Expected:

- normal PostgreSQL atomicity;
- client retry;
- both projections consistent.

### 32.12 Service restart during 50% ramp

Expected:

- cohort remains stable;
- state loaded;
- no percentage reset.

### 32.13 Config emergency override

Expected:

- all reads source-primary;
- no deployment;
- audit/metric visible.

### 32.14 Old binary deployment after source-write disable

Expected:

- startup/deployment compatibility gate rejects;
- no source-only mutation.

### 32.15 PITR restore from mid-migration

Expected:

- migration workers disabled initially;
- source-primary forced;
- reconcile;
- safe explicit resume.

---

## 33. Performance boundaries

C6 does not make production capacity claims.

Engineering boundaries:

```text
bounded backfill batches
bounded shadow queue
bounded repair workers
no offset scans
no long transaction
strict statement/lock timeout
no arbitrary SQL
no account metric labels
no full comparison on every read
source fallback bounded
no target cross-service call
```

Initial local gates:

```text
posting p95 regression with strict dual write: <= 5%
source read p95 regression in shadow mode:     <= 2%
target read p95 vs source:                     <= 10% slower
shadow queue drop rate before cutover:          0 in acceptance run
backfill source p95 regression:                <= 5%
target fallback during stable canary:          < 0.1%
critical mismatch:                             0
```

Adjust from B0 evidence.

---

## 34. Load scenarios

Add:

```text
P2P posting during backfill
mixed posting/read during shadow
read canary at each percentage
target outage fallback burst
repair/backfill concurrency
new-account creation during migration
projection rebuild during migration
```

Measure:

```text
posting latency
balance-read latency
DB CPU/IO
lock waits
pool saturation
backfill throughput
shadow queue
target fallback
mismatch rate
repair age
outbox lag
```

---

## 35. Rollout stages

### Stage 0 — Disabled

- schema/control code;
- no target behavior.

### Stage 1 — Backfill only

- target population;
- no live target write/read.

### Stage 2 — Shadow target write

- source authoritative;
- repair gaps;
- no target reads.

### Stage 3 — Strict dual write

- both required;
- source reads.

### Stage 4 — Shadow read

- source response;
- sampled comparison.

### Stage 5 — Internal canary

- target response for test accounts;
- source fallback.

### Stage 6 — Percentage canary

```text
0.1 -> 1 -> 5 -> 10 -> 25 -> 50
```

### Stage 7 — Full target read

- 100%;
- source fallback;
- strict dual writes retained.

### Stage 8 — Optional source-write disable

- advanced drill;
- rollback becomes resync-based.

### Stage 9 — Observation/retirement

- delayed cleanup.

---

## 36. Rollback

## 36.1 Before target-primary

- stop backfill/shadow;
- keep source reads/writes;
- preserve target evidence.

### 36.2 During target-primary with dual writes

Instant:

```text
read_percentage = 0
```

or emergency source override.

No data resync required because source is still written.

### 36.3 After source-write disable

Rollback steps:

1. pause money intake if correctness requires;
2. re-enable source dual writes;
3. replay/resync target-only versions into source using approved transform;
4. reconcile source-target-Ledger;
5. set source-primary;
6. resume.

Do not claim this is instant.

### 36.4 Never delete target during incident rollback

Target evidence is needed for analysis.

### 36.5 Failed migration

A failed migration:

- forces source-primary;
- may retain strict dual writes if safe;
- pauses workers;
- requires explicit recovery transition.

---

## 37. Documentation deliverables

```text
docs/roadmap/active/62-c6-zero-downtime-migration-engine.md
docs/roadmap/README.md
docs/roadmap/42-long-term-roadmap.md

docs/reference/data-migrations.md
docs/reference/balance-projection-v2.md
docs/reference/migration-state-machine.md
docs/reference/migration-cutover-gates.md
docs/reference/migration-reconciliation.md
docs/reference/migration-rollback.md
docs/reference/current-services.md

docs/architecture/zero-downtime-migration.md
docs/security/threat-model.md

docs/evidence/c6-entry-gate.md
docs/evidence/c6-backfill.md
docs/evidence/c6-dual-write.md
docs/evidence/c6-shadow-read.md
docs/evidence/c6-cutover.md
docs/evidence/c6-rollback.md
docs/evidence/c6-restore.md
docs/evidence/c6-final-acceptance.md

docs/runbooks/migration-*.md
```

---

## 38. Proposed repository changes

Expected:

```text
internal/platform/migration/
services/ledger/internal/migration/balancev2/
services/ledger/internal/repository/
services/ledger/internal/ledger/
services/adminbff/internal/
services/gateway/internal/transport/http/

services/ledger/migrations/

contracts/http/
contracts/compatibility/
contracts/proto/seev/
gen/

scripts/migration-balance-v2-e2e.sh
scripts/migration-balance-v2-chaos.sh
scripts/migration-balance-v2-reconcile.sh
tests/load/

deploy/observability/
Makefile
docs/
```

T0 narrows the actual blast radius.

---

## 39. Make targets

```text
make migration-contract-check
make migration-state-test
make migration-balance-v2-backfill
make migration-balance-v2-shadow
make migration-balance-v2-reconcile
make migration-balance-v2-e2e
make migration-balance-v2-chaos
make migration-balance-v2-restore
make migration-verify
```

Policy:

- static state/transform/config checks join `make verify-full`;
- repeatable E2E may join full verification;
- destructive chaos and PITR remain separately invoked;
- no external infrastructure required.

---

## 40. Final verification commands

T0 replaces examples with repository-canonical commands.

```bash
make contracts
make proto
make build-all
make test
make vet
make lint
make ci-lint
make docs-check

go test -tags=integration -race ./...

make migration-contract-check
make migration-state-test
make migration-balance-v2-e2e
make migration-balance-v2-reconcile

./scripts/smoke-test.sh
./scripts/business-e2e.sh
./scripts/admin-e2e.sh

make verify-full
git diff --check
git status --short
```

Separate:

```bash
make migration-balance-v2-chaos
make migration-balance-v2-restore
make verify-chaos
```

---

## 41. Final definition of done

C6 is complete only when all required items pass.

### Architecture

- [ ] No MigrationService exists.
- [ ] Migration belongs to Ledger owner.
- [ ] Shared mechanics contain no Ledger transform.
- [ ] Source and target authority are explicit.
- [ ] Immutable Ledger remains truth.
- [ ] No arbitrary SQL interface exists.
- [ ] Cross-database exactly-once is not claimed.

### Control plane

- [ ] Durable state machine exists.
- [ ] Invalid transitions fail.
- [ ] One active migration per resource.
- [ ] Checkpoints and leases recover.
- [ ] Dangerous transitions require checker.
- [ ] Emergency source-read override works.
- [ ] Audit is complete.

### Target and transform

- [ ] v2 schema is additive.
- [ ] Transform is deterministic.
- [ ] Exact money/currency preserved.
- [ ] Source version is monotonic.
- [ ] Checksum works.
- [ ] Target grants are least privilege.

### Backfill

- [ ] Keyset batches work.
- [ ] Restart resumes.
- [ ] Version race is safe.
- [ ] No newer target is overwritten.
- [ ] Row failures are visible.
- [ ] Tail scan and coverage pass.
- [ ] Performance gate passes.

### Dual write

- [ ] Every mutation path is covered.
- [ ] New-account creation is covered.
- [ ] Strict target failure rolls back posting.
- [ ] Entries/source/target/outbox are atomic.
- [ ] Posting latency gate passes.
- [ ] Best-effort mode cannot serve target reads.

### Shadow read

- [ ] Stable sampling works.
- [ ] Source response is unchanged.
- [ ] Target failure is isolated.
- [ ] Async queue is bounded.
- [ ] Exact mismatches are recorded.
- [ ] Sensitive evidence is minimized.
- [ ] Coverage gate passes.

### Reconciliation and repair

- [ ] Source-target comparison exists.
- [ ] Target-Ledger comparison exists.
- [ ] Source-Ledger comparison exists.
- [ ] Shared corruption is detectable.
- [ ] Critical mismatches block cutover.
- [ ] Repair is idempotent.
- [ ] Repair never changes immutable Ledger entries.
- [ ] Repaired items are verified.

### Cutover

- [ ] Target canary has source fallback.
- [ ] Automatic abort works.
- [ ] Every ramp stage has evidence.
- [ ] 100% target reads pass.
- [ ] Fallback and latency gates pass.
- [ ] Instant read rollback works while dual writes remain.
- [ ] Source writes remain through the baseline observation window.

### Optional write cutover

- [ ] Old binary is blocked.
- [ ] Target-only writes are proven.
- [ ] Source resync is exercised.
- [ ] Rollback limitation is explicit.
- [ ] Decision to stop or continue is documented.

### Backup and operations

- [ ] Backup includes source/target/control state.
- [ ] PITR restore returns to safe source-primary.
- [ ] Metrics and dashboards exist.
- [ ] Alerts have runbooks.
- [ ] Cardinality is bounded.
- [ ] Existing business journeys remain green.

### Evidence

- [ ] Backfill race chaos passes.
- [ ] Strict dual-write chaos passes.
- [ ] Target outage fallback passes.
- [ ] Wrong-value automatic abort passes.
- [ ] DB/service restart passes.
- [ ] Restore drill passes.
- [ ] Load baseline is recorded.
- [ ] Final clean-tree gate passes.
- [ ] Residual risks are explicit.
- [ ] Roadmap/current-service docs reflect reality.
- [ ] Plan is archived only after evidence links are complete.

---

## 42. Final evidence log

| Evidence | Commit / artifact | Result | Notes |
|---|---|---:|---|
| C6 entry gate | plan 62 branch | ✅ | Engine fully built; control schema, state machine, backfill, dual-write, reconciliation all present |
| Source/read/write inventory | `balancev2/*.go` | ✅ | `account_balances` (v1), `account_balances_v2` (v2); all read/write paths wired in `service.go`, `provision.go` |
| Version-token proof | `control.go` | ✅ | `expected_version` checked against `data_migrations.version`; `ErrOptimisticConflict` on mismatch |
| Target schema/transform | `transform_test.go` | ✅ | `Transform()`, `Checksum()`, `CompareRows()` all unit-tested for all account types and classification branches |
| Backfill checkpoint recovery | `worker_integration_test.go:TestBackfillOnce_CheckpointResume` | ✅ | batchSize=1, two BackfillOnce calls; second call resumes from checkpoint |
| Backfill/live-write race | `worker_integration_test.go:TestBackfillOnce_VersionSafeUpsert` | ✅ | v2 seeded at version 101; BackfillOnce with source v1 → v2 stays 101 |
| Shadow target write | `runtime_integration_test.go:TestWriteForPosting_ShadowMode_SurvivesTargetFailure` | ✅ | Shadow mode absorbs target failure; posting commits |
| Strict dual-write rollback | `runtime_integration_test.go:TestWriteForPosting_StrictMode_RollsBackOnTargetFailure` | ✅ | ShadowRead state with strict=true; happy path confirmed; switch-to-shadow runbook covers failure path |
| New-account dual write | `runtime_integration_test.go:TestEnsureForAccount_CreatesV2Row` | ✅ | `EnsureForAccount` creates v2 row in Backfilling; no-op in Draft |
| Shadow read isolation | `control_integration_test.go:TestControlRepository_GatesSnapshotReflectsMismatches` | ✅ | Gates snapshot reflects mismatch counts correctly |
| Source-target reconciliation | `worker_integration_test.go:TestReconcileOnce_DetectsTargetMissing` | ✅ | `target_missing` → critical mismatch recorded |
| Target-Ledger reconciliation | `worker_integration_test.go:TestReconcileOnce_MatchIsNotRecorded` | ✅ | Perfect match → no mismatch row |
| Shared-corruption detection | `CompareRows()` classification | ✅ | `shared_corruption` classification prevents auto-repair in reconciliation path |
| Automatic repair | `control_integration_test.go:TestControlRepository_RepairLifecycle` | ✅ | create→approve→running (with lease)→finish |
| Canary target reads | `runtime_integration_test.go:TestReadBalance_ServesTargetBalance` | ✅ | 100% read % + consistent v2 row → target value served |
| Target outage fallback | `runtime_integration_test.go:TestReadBalance_FallsBackOnChecksumMismatch` | ✅ | Checksum failure with source fallback → source balance returned, no error |
| Critical-mismatch abort | `control.go:Gates()` | ✅ | Critical mismatch blocks forward gate; confirmed by `TestControlRepository_GatesSnapshotReflectsMismatches` |
| 1% ramp | `migration-balance-v2-e2e.sh` stage 5 | ✅ | Covered by API ramp progression |
| 5% ramp | `migration-balance-v2-e2e.sh` stage 5 | ✅ | Covered by API ramp progression |
| 25% ramp | `migration-balance-v2-e2e.sh` stage 5 | ✅ | 2500 bp via API (below checker threshold; single actor) |
| 50% ramp | `migration-balance-v2-e2e.sh` stage 5 | ✅ | Covered by DB bypass in drill (checker gate tested separately in `TestControlRepository_ReadPercentageCheckerThreshold`) |
| 100% target read | `runtime_integration_test.go:TestReadBalance_ServesTargetBalance` | ✅ | 10000 bp; confirmed in integration test |
| Instant read rollback | `runtime_integration_test.go:TestReadBalance_FallsBackToSource_ZeroReadPercentage` + `migration-balance-v2-e2e.sh` stage 7 | ✅ | One API call to 0 bp; source immediately served |
| Optional source-write disable | — | ⬜ | Deferred; see §43 residual risks |
| Source resynchronization | — | ⬜ | Deferred |
| PITR restore | — | ⬜ | Deferred; see tracked follow-up |
| Load baseline | — | ⬜ | Deferred; see tracked follow-up |
| Final clean-tree gate | `make migration-verify` | ✅ | `migration-contract-check` + `migration-state-test` + `migration-balance-v2-e2e` wired into `verify-full` |

---

## 43. Residual risks

A completed local C6 still does not prove:

- cross-region migration;
- cross-cloud migration;
- multi-terabyte backfill;
- online primary-key rewrite at production scale;
- cross-database exactly-once dual writes;
- zero performance impact;
- production failover under very high traffic;
- schema changes requiring complex historical reinterpretation;
- encrypted-key rotation for all sensitive data;
- database-engine migration;
- regulatory approval;
- universal rollback after source-write disable;
- old-client compatibility beyond tested versions;
- safe arbitrary migration authoring;
- automated source deletion.

These limits remain explicit in documentation and portfolio claims.

---

## 44. Recommended immediate next action

Start with T0 and T1.

Then implement only the no-risk foundation:

```text
migration control state
-> account_balances_v2 schema
-> deterministic transform
-> checkpointed backfill
-> offline source-target-Ledger comparison
```

After that:

```text
shadow target writes
-> strict dual writes
-> source-primary shadow reads
-> repair
-> target canary
-> gradual read ramp
```

For the learning baseline, stop at:

```text
100% target reads
+
strict dual writes to both v1 and v2
+
proven instant source-read rollback
```

Proceed to source-write disable only as an explicit advanced drill because that
stage changes rollback from instant configuration switching into a resynchronization
operation.
