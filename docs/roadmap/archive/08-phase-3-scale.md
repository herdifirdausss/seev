# 08 — Phase 3: Scale and Compliance (P2–P3 Features)

> **Superseded (2026-07-12):** Use documents 17–20 rather than this file.
> Detailed execution plans follow the decisions in [13-p1-backlog-review.md](13-p1-backlog-review.md):
> S1+S9 → [17](17-phase3a-policy-recovery.md), S2 → [18](18-phase3b-multi-currency.md),
> S3+S8 → [19](19-phase3c-scheduled-accrual.md), and S6+S7 →
> [20](20-phase3d-aml-reporting.md). **S4 and S5 intentionally have no
> document yet** because they are measurement-gated: S4 waits for lock-wait
> evidence during delta application, while S5 waits for `ledger_entries` to
> approach roughly 50 million rows. Write those documents only when metrics
> demonstrate the need.

The decisions for S1–S9 are locked in [13, section K-S](13-p1-backlog-review.md)
(2026-07-11), including cross-item prerequisites and required patterns such as
the optional-Redis fallback from 12-T1 and deterministic idempotency keys for
S3/S8. Hard prerequisites: S5 needs H3 ([15 T1](15-phase2e-snapshots-statements.md));
S7 needs H2, H3, and H8 ([15](15-phase2e-snapshots-statements.md) and
[16](16-phase2f-governance-recon-rls.md)); S9 has no prerequisite.

This is an outline only; execution details are in documents 17–20. Priorities
are listed from highest to lowest.

## S1 — Limits and velocity (policy layer)

> **The basic global amount cap moved to Task T5 in [10-phase2a-security-gating.md](10-phase2a-security-gating.md).**
> It was implemented early because having no cap at all is a critical finding,
> not merely a scaling feature. S1 remains relevant as the more granular layer:
> per-user, per-type, and daily/monthly velocity limits, to be implemented
> after T5.

Create `internal/policy` with per-transaction, daily, and monthly limits per
user and transaction type. Evaluate them **before** `ledger.Post` in the
transport layer. Store counters in Redis with a sliding window per user/type,
or in memory when `REDIS_ENABLED=false` (use the fallback pattern from
[12-phase2c-resilience-ops.md](12-phase2c-resilience-ops.md) Task T1). Store
limit configuration in a table. The ledger itself remains unaware of policy.

## S2 — Multi-currency

- Remove the hard-coded `IDR` assumption from provisioning and validation. Add
  a `currencies` table (`code`, `minor_unit`) as the validation source.
- FX is not a ledger feature. Orchestrate `money_out(IDR)` and `money_in(USD)`
  through a conversion account per pair, storing the rate and quote ID in both
  transactions' metadata. Add `fx_conversion` system accounts per currency
  pair.
- Update `GetSystemAccountID` to filter by currency (the TODO from 05 Task
  1b.1).

## S3 — Scheduled and batch posting

- Add `scheduled_transactions` and a `pkg/scheduler` job. Execute ordinary
  `ledger.Post` calls with deterministic keys such as
  `sched:<id>:<run_date>`.
- Batch disbursement reads one file or manifest, posts each item with progress
  tracking, supports resume, and reports the result for every item.

## S4 — Further hot-account mitigation

> **Prerequisite:** Task T1 in [11-phase2b-efficiency-locking.md](11-phase2b-efficiency-locking.md)
> must first separate user and system locks and implement atomic delta
> application. T1 removes `FOR UPDATE` from system accounts entirely, resolving
> most hot-row pressure at MVP scale. S4 is only for a specific system account
> that remains a bottleneck at much larger scale. Measure lock waits and
> row-level contention on `UPDATE ... balance = balance + $delta`; do not use
> the already-removed `FOR UPDATE` path as the signal.

Gateway sharding already exists in `processors.go`. If one system account is
still a bottleneck, measure first, then consider sub-shards such as
`fee['gopay#0'..'#7']` with an aggregate finance view, or an asynchronous system
account balance projection while entries remain synchronous.

## S5 — Partitioning and archival

Follow the six-phase PARTITIONING guide in
`docs/design/legacy-schemas/ledgernew.sql` (dual-write → backfill → rename):
partition `ledger_entries` and `ledger_transactions` monthly by `created_at`.
H3 snapshots must already be running so balance queries do not scan old
partitions. Move partitions older than N months to cold storage while keeping
them immutable and auditable.

## S6 — AML and fraud hooks

Add a `PrePostHook` to the `Handle()` pipeline after business validation and
before entry construction. Start with simple anomaly-velocity and amount-
threshold rules plus `monitor` and `block` modes. Add vendor screening behind
the same interface later.

## S7 — Regulatory reporting

Produce periodic fund-position and movement reports in the format required by
BI/OJK once the legal entity is clear. Use H3 snapshots and H2 reconciliation
as the data sources, and use the H8 `app_readonly` role.

## S8 — Interest and yield accrual

A daily job calculates accrual for savings-product accounts and posts
`interest_accrue` transactions through a new processor. The registry pattern
makes the new transaction type inexpensive. Capitalize periodically.

## S9 — Point-in-time rebuild and DR drill

Add a script that rebuilds `account_balances` from `ledger_entries` (truncate
the projection and replay entries), plus a scheduled staging drill:
restore a backup → rebuild → run a clean verifier → record the RTO.
