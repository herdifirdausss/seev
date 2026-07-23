# 13 — Backlog Review: Locked Decisions for the Remaining 07/08 Work

Audit date: 2026-07-11, after 10–12 and chaos test T7 were completed and
verified.

This document records an audit of the remaining tasks in [07-phase-2-hardening.md](07-phase-2-hardening.md)
(H1–H8) and [08-phase-3-scale.md](08-phase-3-scale.md) (S1–S9). Like
[09-hardening-review.md](09-hardening-review.md), it locks design decisions so
the execution documents ([14](14-phase2d-ledger-semantics-events.md),
[15](15-phase2e-snapshots-statements.md), and
[16](16-phase2f-governance-recon-rls.md)) can be implemented without reopening
the design debate. Every decision is evaluated for long-term correctness,
limited-resource efficiency, and security.

Read the verified lessons in [09](09-hardening-review.md) first. Integration
tests are mandatory for SQL and locking work; sqlmock alone is not enough.

## New findings

### N1 — Double-reversal race (critical: money creation)

`Reversal.Validate` reads the original transaction with an ordinary SELECT.
Two concurrent reversals can both see `posted`, both pass validation, and both
post reversing entries. The original can be reversed twice because the later
status update has no `WHERE status='posted'` guard.

**Design consequence:** H7 must use one conditional atomic UPDATE. The second
concurrent winner must receive an error.

### N2 — `external_ref` and metadata are not persisted

H2 requires matching settlement reports to `ledger_transactions`, but the
canonical table has no metadata or reference column. `Command.Metadata` exists
only in memory and in the transient outbox payload; `Command.ReferenceID` is
also not stored.

**Design consequence:** persist `external_ref` and `gateway` first (K5). The
same data is needed by statements (H4) and reporting (S7).

### N3 — Settle/cancel/release lifecycle operations have no guard

`WithdrawSettle`, `WithdrawCancel`, and `EscrowRelease` validate balances but do
not inspect the original transaction or require `ReferenceID`. A settle after a
cancel may accidentally consume funds held for another active withdrawal,
because holds are aggregated per user.

**Design consequence:** require `ReferenceID` for every lifecycle-closing type
and use the same atomic guard as N1.

## Current status by task

| Task | Already present | Still required |
|---|---|---|
| H1 event contract | Ad-hoc processor payloads and at-least-once delivery | Versioned event types, constants, shared payloads, and consistent processors |
| H2 reconciliation | `external_ref` is allowlisted but discarded | Correlation persistence, recon tables, CSV import, matcher, suspense accounts, and resolution flow |
| H3 snapshots | Daily verifier and scheduler exist | Snapshot table, daily job, `as_of` API, and snapshot-aware verification |
| H4 statements | Account entries and balance endpoints with ownership checks | Period statement endpoint with opening/closing balances and CSV |
| H5 maker-checker | Adjustment processors and internal admin gating | Pending table, request/approve/reject endpoints, distinct identities, audit trail |
| H6 source/destination | Positional fields and a fixed AccountIDs sort | Explicit semantic contract and tests for all processors |
| H7 lifecycle guard | Reversal status check, vulnerable to N1 | Atomic `closed_by_tx_id` guard, required references, and reversal-of-reversal rejection |
| H8 RLS | Complete legacy design in `ledgernew.sql` | Port to canonical/new tables, use `app_service`, and test grants |

## Locked decisions

### K1 — Execution order: 14 → 15 → 16

- **14: H6 → H7 → H1.** Event payloads need source/destination semantics; H7
  closes the two money-safety holes; H1 must lock the event contract before
  external consumers exist.
- **15: H3 → H4.** Statements need snapshot opening balances, and snapshots
  reduce verifier cost for old accounts.
- **16: H5 → H2 → H8.** Reconciliation resolution uses governed adjustments,
  so maker-checker comes first. RLS comes last because it must cover every final
  table.
- S-track work remains behind H-track work.

### K2 — Return explicit resolved-account semantics

Replace the plain account-ID slice with:

```go
type ResolvedAccounts struct {
    Ordered     []uuid.UUID // preserves the positional order used by BuildEntries
    Source      uuid.UUID   // uuid.Nil when not applicable
    Destination uuid.UUID   // uuid.Nil when not applicable
}
```

All 22 processors must populate `Source` and `Destination`. `Ordered` keeps
existing positional entry construction working. The service must assert that
non-nil source/destination IDs are members of `Ordered` and must populate the
transaction header from these fields, not from `SafeIndex`. Reversal may leave
both fields nil because it is multi-leg and ambiguous.

### K3 — One lifecycle guard: `closed_by_tx_id` and a conditional UPDATE

Add:

```sql
closed_by_tx_id UUID NULL UNIQUE REFERENCES ledger_transactions(id),
closed_reason   TEXT NULL CHECK (closed_reason IN
                  ('reversed','settled','cancelled','released','refunded')),
CHECK ((closed_by_tx_id IS NULL) = (closed_reason IS NULL))
```

Close the original with one statement:

```sql
UPDATE ledger_transactions
SET closed_by_tx_id=$new,
    closed_reason=$reason,
    status=CASE WHEN $reason='reversed' THEN 'reversed' ELSE status END,
    updated_at=now()
WHERE id=$reference AND closed_by_tx_id IS NULL;
```

Require `RowsAffected == 1`; otherwise return `ErrAlreadyClosed` (HTTP 409).
Require `ReferenceID` for every lifecycle-closing type, verify the original
type/status/amount, allow full-amount operations only for MVP, and reject a
reversal whose original type is `reversal`. A separate `holds` table is not
needed yet; nullable columns provide the guard without adding a hot-path join.

### K4 — One generic versioned event contract

Create `internal/ledger/events` as the only ledger subpackage that other
modules may import. It contains payload types and constants only. Use
`ledger.transaction.posted.v1` and `ledger.transaction.reversed.v1`; consumers
filter on `transaction_type` rather than 22 routing keys.

Payload fields: `schema_version`, `tx_id`, `transaction_type`, string `amount`,
`currency`, nullable source/destination IDs, compact entries, `occurred_at`,
and `external_ref` when present. Delivery is at least once; consumers must
deduplicate by AMQP `message_id` (`outbox_events.id`). Existing event names may
change now because no external consumers exist yet.

### K5 — Persist correlation before reconciliation

1. Add nullable `external_ref` and `gateway` to `ledger_transactions`, with a
   partial `(gateway, external_ref)` index. Validate `external_ref` (maximum 128
   characters) and the gateway allowlist before storing it.
2. Add `recon_batches` and `recon_items`, including match statuses
   `matched`, `missing_internal`, `missing_external`, and `amount_mismatch`.
3. Add admin-gated internal CSV upload and a synchronous per-batch matcher.
4. Seed a negative-capable `suspense:<gateway>` system account per gateway;
   resolve differences through maker-checker adjustments.
5. Never auto-resolve `missing_external`; report it for a human decision.

### K6 — Incremental, timezone-pinned snapshots

Use `account_balance_snapshots` with a primary key of `(account_id, as_of_date)`.
Run the job at 00:15 Asia/Jakarta using the scheduler lock. Calculate each day
from the previous snapshot plus that day's entries; do not rewrite inactive
accounts. Cross-check the closing balance and alert on discrepancies. The
`as_of` API uses the latest snapshot plus a delta, not a full replay.

### K7 — Strict public statements

Expose `GET /accounts/{id}/statement?from=&to=&format=json|csv` on the public
router with ownership checks. Limit the range to 92 days and 5,000 entries;
stream CSV and return 400 when the limit is exceeded. Use the K6 snapshot for
the opening balance.

### K8 — Minimal but strict maker-checker

Use `pending_adjustments` with requester, approver, reason, status, and the
executed transaction ID. Create, approve, and reject routes live on the
internal admin router. `approved_by` must differ from `requested_by`. Approval
posts with deterministic key `adj:<pending_id>` so retries are idempotent.
Direct `adjustment_credit/debit` posting is removed; urgent freeze/chargeback
operations remain direct but require a reason.

### K9 — RLS as defense in depth

Port the legacy RLS design to canonical and new tables. `app_service` receives
minimal CRUD; it still cannot update/delete immutable ledger entries.
`app_readonly` receives read access except sensitive outbox/pending payloads.
Enable and force RLS now as a privilege boundary, not as tenant isolation.
Move the application connection to `app_service` and test the grants.

### K-S — S-track decisions

- **S1:** policy limits run before `ledger.Post`; Redis or the in-memory
  fallback holds counters. The ledger remains unaware of policy.
- **S2:** add a currency registry and currency-aware system-account lookup; FX
  is orchestration across two ledger transactions.
- **S3:** scheduled transactions use `sched:<id>:<run_date>` keys; batch
  disbursement is a resumable loop of ordinary `Post` calls.
- **S4:** wait for lock-wait evidence before adding hot-account sub-shards.
- **S5:** follow the six-phase partitioning guide only after H3 and roughly 50
  million ledger-entry rows.
- **S6:** add monitor-then-block AML hooks after S1.
- **S7:** reporting depends on H2, H3, and H8.
- **S8:** add a registered accrual transaction and daily idempotent job.
- **S9:** rebuild projections from entries and record an RTO in a staging drill.

## Contracts that remain unchanged

The section E contracts from 09 remain mandatory: idempotency handling,
at-least-once outbox delivery, database immutability guards, deterministic SQL
lock ordering, and graceful shutdown. Also preserve the user/system lock split,
server-side fees and metadata allowlist, per-user idempotency scope, and outbox
backoff/replay from 10–12.

## Verification for tasks 14–16

```bash
go build ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all
```

Migration tasks must test both up and down paths. New endpoints require manual
curl smoke tests against the Docker stack.
