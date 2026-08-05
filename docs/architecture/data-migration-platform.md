# C6 zero-downtime data migration platform

C6 is the internal framework for migrating data between storage projections
without a maintenance window, a read outage, or a financial safety regression.
The Ledger's `account_balances` → `account_balances_v2` migration is the
reference implementation; the platform packages
(`internal/platform/migration/*`) are generic and can drive any service's
projection migration.

## Boundary

The migration control plane lives entirely inside the service that owns the
data. The Admin BFF proxies operator requests through to the Ledger's internal
admin router; no migration logic runs in the BFF. External callers never see
the target table directly until the cutover is complete.

```mermaid
flowchart LR
  subgraph Operator
    BFF[Admin BFF\nmigrations console]
  end
  subgraph Ledger
    API[migration_http.go\nadmin routes]
    CTRL[ControlRepository\nstate machine]
    WRK[lifecycleWorker\nBackfillOnce / ReconcileOnce]
    RT[Runtime\nWriteForPosting / ReadBalance]
    SRC[(account_balances\nv1 source)]
    TGT[(account_balances_v2\ntarget)]
    CTL[(data_migrations\ndata_migration_checkpoints\ndata_migration_mismatches\ndata_migration_repairs)]
  end
  BFF -->|POST /transition, /read-percentage, /reconcile| API
  API --> CTRL
  CTRL --> CTL
  WRK --> CTRL
  WRK -->|BackfillOnce| SRC
  WRK -->|upsert| TGT
  RT -->|dual write| SRC
  RT -->|dual write| TGT
  RT -->|ReadBalance| TGT
```

## State machine

The migration advances through 17 named states. Forward transitions are
operator-driven (maker/checker) or automatic (backfill completion). Only one
migration per name may be active at a time; the lifecycle-machine is
deliberately not generalized to a multi-migration pipeline.

```mermaid
stateDiagram-v2
  [*] --> Draft
  Draft --> Validated : operator (maker)
  Validated --> TargetReady : operator (maker)
  TargetReady --> Backfilling : operator (maker)
  Backfilling --> DualWriteShadow : auto when backfill empty page
  DualWriteShadow --> ShadowRead : operator (maker)
  ShadowRead --> CanaryRead : operator (maker)\ngate: shadow_success_ratio ≥ 99.99%
  CanaryRead --> RampingRead : operator (maker)\ngate: canary_read_ratio ≥ 99.99%
  RampingRead --> TargetPrimary : operator (maker)\n(after ramp to 100%)
  TargetPrimary --> SourceWriteDisabled : operator (maker+checker)
  SourceWriteDisabled --> Observation : operator (maker+checker)
  Observation --> Completed : operator (maker+checker)

  DualWriteShadow --> Paused : operator (maker)
  ShadowRead --> Paused
  CanaryRead --> Paused
  RampingRead --> Paused
  Paused --> DualWriteShadow : operator (maker)
  Paused --> ShadowRead
  Paused --> CanaryRead
  Paused --> RampingRead

  DualWriteShadow --> RollingBack : operator (maker+checker)
  ShadowRead --> RollingBack
  CanaryRead --> RollingBack
  RampingRead --> RollingBack
  TargetPrimary --> RollingBack
  RollingBack --> RolledBack
  RolledBack --> [*]

  Backfilling --> Failed : unrecoverable error
  Failed --> [*]

  Draft --> CancelledBeforeWrite
  CancelledBeforeWrite --> [*]
```

Dangerous transitions — `SourceWriteDisabled`, `Observation`, `Completed`,
`RollingBack`, and any read-percentage increase above 2500 bp (25%) — require
a second distinct actor (`approve: true` in the request body with checker
privileges). Self-approval is rejected at the server-side `ControlRepository`.

## Live dual write

During `Backfilling`, `DualWriteShadow`, and all shadow/read states, every
posting transaction writes both projections inside the same database
transaction. Failure mode is controlled by `StrictDualWrite`:

- **Shadow mode** (`StrictDualWrite=false`): target write failure is absorbed
  and recorded as a shadow-write gap; the posting succeeds.
- **Strict mode** (`StrictDualWrite=true`, automatically set at `ShadowRead`):
  target write failure rolls back the whole posting transaction.

```mermaid
sequenceDiagram
  participant Caller as posting caller
  participant TX as DB transaction
  participant V1 as account_balances (v1)
  participant V2 as account_balances_v2 (v2)
  participant GAP as shadow_write_gaps

  Caller->>TX: BEGIN
  TX->>V1: UPDATE balance, bump version
  TX->>V2: UPSERT WHERE source_version < EXCLUDED.source_version
  alt StrictDualWrite=true
    V2-->>TX: error → ROLLBACK
    TX-->>Caller: error (posting rejected)
  else StrictDualWrite=false (shadow)
    V2-->>TX: error → record gap
    TX->>GAP: INSERT gap record
    TX-->>Caller: COMMIT (posting accepted)
  end
```

## Backfill / live-write race prevention

The backfill keyset scan and the live dual-write path can operate
concurrently. A live posting may have advanced the target row's
`source_version` beyond what the backfill scan is currently processing. The
version-safe upsert prevents the backfill from overwriting a newer live row:

```mermaid
sequenceDiagram
  participant BKF as BackfillOnce
  participant DW as dual-write (posting)
  participant V1 as account_balances
  participant V2 as account_balances_v2

  Note over V2: row exists: source_version=95 (from earlier backfill page)
  DW->>V1: UPDATE → version=101
  DW->>V2: UPSERT → source_version=101
  Note over V2: row now: source_version=101

  BKF->>V1: SELECT … WHERE id > last_key (reads version=101)
  BKF->>V2: INSERT … ON CONFLICT DO UPDATE\nSET … WHERE source_version < EXCLUDED.source_version
  Note over V2: 95 < 101 is false → row unchanged\nsource_version stays 101
```

The `WHERE source_version < EXCLUDED.source_version` clause in the upsert is
the sole guard. It is covered by `TestBackfillOnce_VersionSafeUpsert`.

## Read ramp and instant rollback

`ReadBalance` uses a stable SHA-256 cohort hash to assign each account to a
bucket (0–9999 bp). An account is served from the target only if its bucket
falls within `read_percentage_basis_points`. Setting the percentage to 0
immediately removes all accounts from the target cohort with no redeploy.

```mermaid
flowchart TD
  A[ReadBalance called] --> B{migration enabled\nand in read state?}
  B -- no --> SRC[return source]
  B -- yes --> C{account in cohort?\nbucket < read_percentage_bp}
  C -- no --> SRC
  C -- yes --> D{target row exists?}
  D -- no --> E{source fallback\nenabled?}
  E -- yes --> SRC
  E -- no --> ERR[return ErrGateBlocked]
  D -- yes --> F{checksum valid?}
  F -- no --> SRC
  F -- yes --> TGT[return target balance]
```

The `read_percentage_basis_points` column can be updated independently of the
state transition, via `POST /admin/migrations/{id}/read-percentage`. Reducing
it to 0 is a one-second, one-API-call instant rollback. This is the primary
safety escape hatch for any production anomaly detected post-cutover.

## Target-primary fallback chain

At `TargetPrimary`, `SourceFallbackEnabled=true` (set at `TargetReady`)
means every read failure silently falls back to the source. The fallback codes
are logged as structured events for post-analysis:

| Reason | Fallback? | Logged event |
|---|---|---|
| Not in cohort (0% ramp) | — | no event |
| Target row missing | yes (if fallback enabled) | `target_missing` |
| Checksum mismatch | yes (if fallback enabled) | `checksum_mismatch` |
| Target read error | yes (if fallback enabled) | `target_read_error` |
| Account not in cohort (partial ramp) | — | no event |

At `SourceWriteDisabled` the fallback is disabled server-side; errors surface
to callers rather than silently serving stale data.

## Three-layer reconciliation

`ReconcileOnce` compares each source row against the target using the
`CompareRows` classifier:

```mermaid
flowchart TD
  R[ReconcileOnce: scan source rows] --> CMP{CompareRows\nsource vs target}
  CMP -->|match| OK[no mismatch recorded]
  CMP -->|target_missing| M1[critical mismatch\nauto-request repair]
  CMP -->|value_mismatch / target_stale| M2[high mismatch\noperator review required]
  CMP -->|target_ahead| M3[warning\ndual-write gap, investigate]
  CMP -->|currency_mismatch\nversion_mismatch| M4[critical mismatch\nflag for repair]

  M1 --> GC{cross-check\nagainst ledger_entries\nfor source corruption?}
  GC -->|source also disagrees\nwith ledger| SCC[reclassify as\nshared_corruption\nskip auto-repair]
  GC -->|source matches ledger| REP[repair eligible]
```

Mismatches are never silently dropped. A critical mismatch (`target_missing`,
`currency_mismatch`) automatically aborts the read path to source, regardless
of `read_percentage_basis_points`, until the repair is verified.

## Repair lifecycle

```mermaid
sequenceDiagram
  participant Maker as Maker (operator)
  participant Checker as Checker (different actor)
  participant CTRL as ControlRepository
  participant V2 as account_balances_v2

  Maker->>CTRL: RequestRepair(mismatch_id, reason)
  CTRL-->>Maker: Repair{status=pending_approval}

  Checker->>CTRL: ApproveRepair(repair_id, account_id, reason)
  CTRL->>CTRL: acquire lease on repair row
  CTRL->>V2: re-transform source row → upsert
  CTRL->>CTRL: mark repair verified, release lease
  CTRL-->>Checker: Repair{status=verified}

  Note over CTRL: self-approval rejected at server side\n(approver must differ from requester)
  Note over CTRL: ReclaimStuckRepairs runs on every\nworker tick (expired lease recovery)
```

## Control schema (abridged)

| Table | Purpose |
|---|---|
| `data_migrations` | one row per named migration; holds state, version, read %, flags |
| `data_migration_checkpoints` | keyset position + lease for each backfill/reconcile worker |
| `data_migration_mismatches` | classified reconciliation findings |
| `data_migration_repairs` | maker/checker repair lifecycle with lease columns |
| `data_migration_read_events` | immutable audit log of every read served from target |
| `shadow_write_gaps` | shadow-mode target write failures, for post-analysis |
| `data_migration_gates` | snapshot of each gate at the time of a state transition |
