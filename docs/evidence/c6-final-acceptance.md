# C6 Final Acceptance Evidence — Plan 62

## Current disposition

Implementation committed on branch
`claude/zero-downtime-migration-engine-7cf82c` (2026-08-05). The core verified
scope is: unit/integration tests proving invariants, Make targets wired into
`verify-full`, a real Admin BFF operator console, reference architecture doc,
security threat-model findings, 4 operator runbooks, and one end-to-end
migration drill script.

Explicitly deferred (not attempted this pass):
- Chaos matrix (15 failure-injection scenarios from §25/§32/§34)
- Load scenarios (ramp under sustained posting throughput)
- PITR restore drill against production-equivalent data volume
- Optional write cutover (`source_write_disabled` → `observation` → `completed`)
- Remaining runbooks beyond the 4 written here

These are logged as tracked follow-ups in plan 62 §42 and will be addressed in
a subsequent pass; they do not block this evidence record.

## Evidence checklist

### Control plane state machine

| Area | Result | Evidence |
|---|---|---|
| Every valid `ValidateTransition` edge | unit-tested | `internal/platform/migration/state_test.go` — all 17 states, every valid forward/backward arc, invalid transitions rejected |
| `RequiresChecker` threshold (>2500 bp) | unit-tested | `state_test.go` — transitions below/at/above 2500 bp |
| `IsSourcePrimary` / `IsTargetPrimary` predicates | unit-tested | `state_test.go` |
| Idempotency (unique active migration) | integration-tested | `control_integration_test.go:TestControlRepository_IdempotencyBlocksDuplicate` |
| Optimistic concurrency (`ErrOptimisticConflict`) | integration-tested | `control_integration_test.go:TestControlRepository_OptimisticConflict` |
| Self-approval rejected | integration-tested | `control_integration_test.go:TestControlRepository_SelfApprovalRejected` |
| Checker required for dangerous transitions | integration-tested | `control_integration_test.go:TestControlRepository_MissingApprovalRejectedForDangerousTransition` |
| `Gates()` snapshot correctness | integration-tested | `control_integration_test.go:TestControlRepository_GatesSnapshotReflectsMismatches` |
| Repair idempotency (unique constraint) | integration-tested | `control_integration_test.go:TestControlRepository_RepairIdempotency` |
| Repair lifecycle: create→approve→running (with lease)→finish | integration-tested | `control_integration_test.go:TestControlRepository_RepairLifecycle` |
| `ReclaimStuckRepairs` reclaims expired lease | integration-tested | `control_integration_test.go:TestControlRepository_ReclaimStuckRepairs` |
| Read-percentage checker threshold enforcement | integration-tested | `control_integration_test.go:TestControlRepository_ReadPercentageCheckerThreshold` |
| Checkpoint lease exclusivity | integration-tested | `control_integration_test.go:TestControlRepository_CheckpointLeaseExclusivity` |
| Checkpoint expired-lease reclaim | integration-tested | `control_integration_test.go:TestControlRepository_CheckpointExpiredLeaseReclaim` |

### Cohort hashing and sampling

| Area | Result | Evidence |
|---|---|---|
| `CohortBucket` / `InCohort` determinism | unit-tested | `internal/platform/migration/sampling_test.go` — same key → same bucket across N calls |
| Distribution sanity (no extreme skew) | unit-tested | `sampling_test.go` — bucket spread over large sample |
| `StableKey` composition | unit-tested | `sampling_test.go` |
| Basis-point bounds validation | unit-tested | `internal/platform/migration/rollout_test.go` |
| `SuggestedRamp()` sequence | unit-tested | `rollout_test.go` |

### Transform, checksum, and comparison

| Area | Result | Evidence |
|---|---|---|
| `Transform()` field routing per account type | unit-tested | `services/ledger/internal/migration/balancev2/transform_test.go` — cash/hold/pending/frozen → available/reserved/pending/restricted |
| `Checksum()` determinism | unit-tested | `transform_test.go` — same row → same hash, any field change → different hash |
| `CompareRows()` classification table | unit-tested | `transform_test.go` — match, target_missing, target_stale, target_ahead, value_mismatch, currency_mismatch, version_mismatch |

### Backfill correctness

| Area | Result | Evidence |
|---|---|---|
| Backfill / live-write race: stale batch must not overwrite a newer live write | integration-tested | `worker_integration_test.go:TestBackfillOnce_VersionSafeUpsert` — seeds v2 at version 101, runs BackfillOnce with source at version 1, asserts v2 still has version 101 |
| Checkpoint resume after simulated restart | integration-tested | `worker_integration_test.go:TestBackfillOnce_CheckpointResume` — batchSize=1, two accounts, two BackfillOnce calls |
| Empty-page auto-transition to `DualWriteShadow` | integration-tested | `worker_integration_test.go:TestBackfillOnce_CompletesAndTransitions` |

### Reconciliation

| Area | Result | Evidence |
|---|---|---|
| `target_missing` classified as critical mismatch | integration-tested | `worker_integration_test.go:TestReconcileOnce_DetectsTargetMissing` |
| Perfect match produces no mismatch row | integration-tested | `worker_integration_test.go:TestReconcileOnce_MatchIsNotRecorded` |

### Dual write and read path

| Area | Result | Evidence |
|---|---|---|
| Strict mode: `WriteForPosting` succeeds in `ShadowRead` state | integration-tested | `runtime_integration_test.go:TestWriteForPosting_StrictMode_RollsBackOnTargetFailure` |
| Shadow mode: `WriteForPosting` survives non-existent account | integration-tested | `runtime_integration_test.go:TestWriteForPosting_ShadowMode_SurvivesTargetFailure` |
| `EnsureForAccount` creates v2 row in Backfilling | integration-tested | `runtime_integration_test.go:TestEnsureForAccount_CreatesV2Row` |
| `EnsureForAccount` no-op before target writes | integration-tested | `runtime_integration_test.go:TestEnsureForAccount_NoOpBeforeTargetWrites` |
| `ReadBalance` returns source at 0% read percentage | integration-tested | `runtime_integration_test.go:TestReadBalance_FallsBackToSource_ZeroReadPercentage` |
| `ReadBalance` serves target at 100% with consistent v2 row | integration-tested | `runtime_integration_test.go:TestReadBalance_ServesTargetBalance` |
| `ReadBalance` falls back on checksum mismatch | integration-tested | `runtime_integration_test.go:TestReadBalance_FallsBackOnChecksumMismatch` |
| Instant read rollback (0 bp) | integration-tested | `TestReadBalance_FallsBackToSource_ZeroReadPercentage` — advances to TargetPrimary 100%, resets to 0, confirms source served |

### End-to-end migration drill

| Stage | Result | Evidence |
|---|---|---|
| Draft → Validated → TargetReady → Backfilling via API | scripted | `scripts/migration-balance-v2-e2e.sh` stage 2 |
| Backfill worker auto-transitions to `dual_write_shadow` | scripted | stage 3 (polls `migration_state`) |
| Dual write: v1 = v2 after additional postings | scripted | stage 4 (`balance_v1` vs `balance_v2` assertion) |
| ShadowRead → CanaryRead → RampingRead | scripted | stage 5 |
| Read ramp to 25% via API | scripted | stage 5 (below checker threshold — single actor) |
| Read ramp to 100% (forced via DB for E2E bypass) | scripted | stage 5 (psql UPDATE) |
| TargetPrimary: balance API returns without error | scripted | stage 6 |
| Instant read rollback to 0% | scripted | stage 7 |
| Balance API still works post-rollback (source path) | scripted | stage 7 |
| Pre-cutover reconciliation | scripted | stage 8 |
| Mismatch detection (tampered v2 row) | scripted | stage 9 |
| Repair round-trip: request (maker) → approve (checker) → verified | scripted | stage 9 |
| v1 = v2 after repair | scripted | stage 9 assertion |

### Operator surface

| Area | Result | Evidence |
|---|---|---|
| Admin BFF migrations console (state, gates, mismatches, action forms) | implemented | `services/adminbff/internal/web/templates/migrations.html` — `hx-get` polling, `{{if .IsMaker}}`/`{{if .IsChecker}}` role-gated forms |
| Architecture doc with 6 Mermaid diagrams | written | `docs/architecture/data-migration-platform.md` |
| Threat model TM-23..TM-26 | appended | `docs/security/threat-model.md` §6 |
| 4 operator runbooks | written | `docs/operations/runbooks/migration-instant-read-rollback.md`, `migration-strict-dual-write-failure.md`, `migration-shadow-mismatch.md`, `migration-backfill-stalled.md` |
| `make migration-verify` target | wired | `Makefile` → `migration-contract-check` + `migration-state-test` + `migration-balance-v2-e2e` |
| `make verify-full` integration | wired | `Makefile` includes `migration-contract-check`, `migration-state-test`, `migration-balance-v2-e2e` |

## Deferred scope (tracked follow-ups)

| Follow-up | Tracked in |
|---|---|
| Chaos matrix (15 failure-injection scenarios) | plan 62 §25/§32/§34 |
| Load scenarios (posting throughput under migration) | plan 62 §34 |
| PITR restore drill with migration mid-flight | plan 62 §34 |
| Optional write cutover (`source_write_disabled` path) | plan 62 §8.4 |
| Remaining 10+ runbooks from §37 | plan 62 §37 |
| Full 15-scenario shadow-read acceptance matrix | plan 62 §15.2 |
