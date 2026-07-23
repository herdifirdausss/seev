# 14 — Phase 2d: Ledger Semantics and Event Contract (H6, H7, H1)

Prerequisite: 10–12 are complete. Read [13-p1-backlog-review.md](13-p1-backlog-review.md),
where K2, K3, K4 and findings N1/N3 are locked. Execute T1 → T2 → T3 because
T3 depends on T1. The verified lessons from 09 apply in full: every SQL or
posting-order change requires integration and smoke tests, not unit tests alone.

## T1 — Explicit source/destination semantics (07 H6, K2)

`execTransfer` currently fills `source_account_id` and
`destination_account_id` from sorted slice positions. Replace this implicit
contract with `ResolvedAccounts`:

```go
type ResolvedAccounts struct {
    Ordered     []uuid.UUID
    Source      uuid.UUID
    Destination uuid.UUID
}
```

Change `ResolveAccounts` to return this type while keeping `Ordered` for the
existing positional `BuildEntries` code. Audit all 22 processors individually:
money in/out, transfers, withdrawals, escrow, adjustments, freezes,
chargebacks, and reversals must identify their semantic source and destination;
reversal may leave both IDs nil when the meaning is ambiguous.

The service must assert that non-nil source and destination IDs belong to
`Ordered`, populate transaction-header columns from the explicit fields, and
remove `SafeIndex`. Regenerate mocks rather than editing them by hand. No data
migration is needed because the header columns are informational and entries
remain authoritative.

### Tests and recorded result (2026-07-11)

- Add table-driven coverage for all 22 processor types.
- Reject a source/destination ID that is not in `Ordered`.
- Post real money-in and P2P transfers and verify semantic header columns.

Completed: all processors were audited, `resolved_accounts_test.go` was added,
membership assertions were added to the service, and the PostgreSQL contract
tests passed. `SafeIndex` is gone from the service and the full build, vet, unit,
integration, and smoke gates were green.

## T2 — Atomic lifecycle guard (07 H7, N1/N3, K3)

Add `000004_lifecycle_guard`:

```sql
ALTER TABLE ledger_transactions
  ADD COLUMN closed_by_tx_id UUID NULL UNIQUE REFERENCES ledger_transactions(id),
  ADD COLUMN closed_reason TEXT NULL CHECK
    (closed_reason IN ('reversed','settled','cancelled','released','refunded')),
  ADD CONSTRAINT chk_closed_pair
    CHECK ((closed_by_tx_id IS NULL) = (closed_reason IS NULL));
```

Add `TransactionRepository.CloseOriginal`, implemented as one conditional
UPDATE with `WHERE id=$1 AND closed_by_tx_id IS NULL`. Require
`RowsAffected == 1`; otherwise return `ErrAlreadyClosed` and map it to HTTP 409.
For a reversal, update `status='reversed'` in the same statement.

Require `ReferenceID` for all lifecycle-closing types. In each validator, load
the original header in the same database transaction and verify its type,
`posted` status, open state, and exact amount. MVP supports full-amount settle,
cancel, release, refund, and reversal only. Reject reversal of a reversal.
Close the original centrally in `execTransfer` using a type-to-reason map; this
keeps the guard in one place and avoids changing `BuildEntries` signatures.

### Tests and recorded result (2026-07-11)

- Concurrent reversal of one original: exactly one succeeds; all other calls
  return `ErrAlreadyClosed`/`ErrAlreadyReversed`.
- Settle after cancel and a second settle are rejected.
- Amount mismatch and missing `ReferenceID` are rejected.
- Up/down migrations, full chaos, build, vet, unit, and integration tests passed.

## T3 — Versioned event contract (07 H1, K4)

Create `internal/ledger/events` with no imports from other ledger subpackages:

```go
const (
    TypeTransactionPosted   = "ledger.transaction.posted.v1"
    TypeTransactionReversed = "ledger.transaction.reversed.v1"
)

type EntrySummary struct {
    AccountID uuid.UUID
    Direction string
    Amount    string
}
```

The posted payload contains `schema_version`, transaction ID/type, string
minor-unit amount, currency, nullable source/destination IDs, entry summaries,
`external_ref`, and `occurred_at`. The reversed payload additionally identifies
the original transaction. Amounts must remain strings in JSON.

Use one adapter to build events for every processor; remove ad-hoc
`map[string]any` payloads. Use the new event type as the AMQP routing key and
document versioning and at-least-once deduplication by
`message_id = outbox_events.id` in `docs/reference/events.md` and `PROJECT_GUIDE.md`.

### Tests and recorded result (2026-07-11)

Golden JSON tests lock the wire format, and the PostgreSQL contract test
unmarshals a real outbox payload into the event struct. RabbitMQ smoke testing
confirmed the new routing key and string amount. All build, vet, unit,
integration, and race gates passed.

## Phase 2d verification

```bash
go build ./... && make test && go test -tags=integration -race ./...
./scripts/chaos-test.sh all
```
