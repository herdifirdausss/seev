# ADR: Zero-downtime data migration via dual-write and read ramp

- Status: Accepted
- Date: 2026-08-05
- Scope: C6 data migration platform

## Context

The Ledger needed to migrate `account_balances` → `account_balances_v2`
without a maintenance window, a read outage, or any financial safety regression.
A simple cut-over (stop writes → migrate → restart) was not acceptable for a
production financial system.

## Decision

The `internal/platform/migration/*` packages implement a 17-state migration
platform that advances through: backfill → dual write → shadow read → canary
read → ramping read → target primary → source write disabled → observation →
completed.

Three key mechanisms:

1. **Version-safe upsert**: backfill uses `WHERE source_version < EXCLUDED.source_version`
   so a concurrent live posting's newer version is never overwritten.

2. **Read ramp with instant rollback**: a stable SHA-256 cohort hash assigns
   accounts to buckets. Setting `read_percentage_basis_points` to 0 is a
   one-second, one-API-call rollback with no redeploy.

3. **Maker/checker on dangerous transitions**: `SourceWriteDisabled`,
   `Observation`, `Completed`, `RollingBack`, and read-percentage increases
   above 25% require a second distinct actor.

Shadow mode (`StrictDualWrite=false`) absorbs target write failures so
postings succeed while gaps are recorded. Strict mode (`StrictDualWrite=true`,
automatic at `ShadowRead`) makes target write failure roll back the posting
— an accepted availability trade-off for data correctness.

## Consequences

Zero-downtime migrations are safe for financial data at the cost of operational
complexity (17 states, maker/checker gates, reconciliation loops). The platform
is generic and reusable for any service projection migration. The reference
implementation is the `account_balances` → `account_balances_v2` migration in
Ledger.
