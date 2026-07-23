# Ledger Event Contract

> [Documentation home](../README.md) · [Reference](README.md)

> **Status: Current. Audience: event producers and consumers.** The Go types
> are authoritative; this page explains their meaning and wire format.

In plain English, Ledger events are receipts announcing facts that Ledger has
already committed. They let notifications, fraud analysis, and other consumers
react without participating in the database transaction that moved money. An
event is delivered at least once, so receiving the same receipt twice must not
repeat the business action.

Wire format for events the ledger module publishes via the transactional
outbox → RabbitMQ (docs/roadmap/archive/14 Task T3, decision K4). The authoritative types
live in [`internal/ledger/events`](../../internal/ledger/events/events.go) — that
package is the **only** subpackage of `internal/ledger` external code may
import (see [Project guide](../development/project-guide.md) Module Boundaries). Import
it directly rather than hand-rolling a decoder from this doc. Definitions of
outbox, idempotency, and at-least-once delivery are in the
[glossary](glossary.md).

## Delivery guarantees

- **At-least-once.** The outbox relay retries with backoff (docs/roadmap/archive/12 Task T2) until the broker confirms publish. A crash between publish and marking `published` re-delivers the same event.
- **Dedup by `message_id`.** Every AMQP message's `message_id` equals `outbox_events.id`. Consumers **must** deduplicate on this id — processing the same id twice must be a no-op on the consumer side.
- **No ordering guarantee** between events, including events about the same transaction or account. Don't assume a `posted` event for transaction A arrives before a later transaction B's event just because A happened first in the ledger.
- **Routing key** = the event type string (e.g. `ledger.transaction.posted.v1`).

## Event types

### `ledger.transaction.posted.v1`

Emitted for every transaction that reaches `status='posted'` — this covers all registered transaction types (`money_in`, `transfer_p2p`, `withdraw_settle`, `reversal` itself, future types like `interest_accrue`, etc.). Consumers that only care about specific transaction types filter on `transaction_type`, rather than subscribing to a per-type routing key — adding a new transaction type to the registry never requires a new event schema.

```go
type EntrySummary struct {
    AccountID uuid.UUID `json:"account_id"`
    Direction string    `json:"direction"` // "debit" | "credit"
    Amount    string    `json:"amount"`    // minor units, decimal string
}

type TransactionPosted struct {
    SchemaVersion        int            `json:"schema_version"` // currently 1
    TxID                 uuid.UUID      `json:"tx_id"`
    TransactionType      string         `json:"transaction_type"`
    Amount               string         `json:"amount"`   // minor units, decimal string
    Currency             string         `json:"currency"`
    SourceAccountID      *uuid.UUID     `json:"source_account_id,omitempty"`      // nil if not a single source->destination pair
    DestinationAccountID *uuid.UUID     `json:"destination_account_id,omitempty"` // nil if not a single source->destination pair
    Entries               []EntrySummary `json:"entries"`
    ExternalRef           string         `json:"external_ref,omitempty"`
    OccurredAt             time.Time      `json:"occurred_at"`
    UserID                *uuid.UUID     `json:"user_id,omitempty"`
    TargetUserID          *uuid.UUID     `json:"target_user_id,omitempty"`
    RequestID             string         `json:"request_id,omitempty"`
}
```

`SourceAccountID`/`DestinationAccountID` are `null` (omitted) when the transaction's movement isn't a single semantic source→destination pair — currently only `reversal`, which can touch more than two accounts (e.g. reversing a transaction that had a fee leg). Use `entries` to reconstruct the full movement in that case; `entries` always reflects the exact double-entry postings, including any fee leg.

`Amount` and every entry's `Amount` are **always JSON strings**, never numbers — this avoids float precision loss in consumers written in languages without arbitrary-precision decimals.

`ExternalRef` is populated only when the poster supplied `metadata.external_ref` on the original request; absent otherwise (`omitempty`).

`UserID` and `TargetUserID` tell user-facing consumers which party or parties
are associated with the posting without requiring a database query back to
Ledger. They are absent for system-only transactions. `RequestID` carries the
originating request correlation into asynchronous processing when available.

### `ledger.transaction.reversed.v1`

Emitted **in addition to** a `ledger.transaction.posted.v1` for the reversal transaction itself, routed against the **original** transaction's aggregate id — so a consumer following one transaction's lifecycle sees this notification without correlating two different aggregate ids.

```go
type TransactionReversed struct {
    SchemaVersion int       `json:"schema_version"` // currently 1
    ReversalTxID  uuid.UUID `json:"reversal_tx_id"`
    OriginalTxID  uuid.UUID `json:"original_tx_id"`
    Amount        string    `json:"amount"`
    Currency      string    `json:"currency"`
    OccurredAt    time.Time `json:"occurred_at"`
}
```

### `ledger.adjustment.decided.v1`

Emitted when an operator adjustment is approved or rejected. A rejection has
no `executed_tx_id` because no money moved.

```go
type AdjustmentDecided struct {
    SchemaVersion int        `json:"schema_version"`
    PendingID     uuid.UUID  `json:"pending_id"`
    RequestedBy   string     `json:"requested_by"`
    ApprovedBy    string     `json:"approved_by"`
    Decision      string     `json:"decision"` // "approved" | "rejected"
    ExecutedTxID  *uuid.UUID `json:"executed_tx_id,omitempty"`
    OccurredAt    time.Time  `json:"occurred_at"`
}
```

## Example: a `money_in` posting

```json
{
  "schema_version": 1,
  "tx_id": "019f5139-9e34-77db-94bf-7f94ba2b841d",
  "transaction_type": "money_in",
  "amount": "100000",
  "currency": "IDR",
  "source_account_id": "00000000-0000-0000-0000-0000000000a1",
  "destination_account_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "user_id": "71f8f71d-6c83-4a9d-b0d5-f93e5183de44",
  "request_id": "019f5139-55cf-71db-8a2f-43b3f90065a4",
  "entries": [
    {"account_id": "00000000-0000-0000-0000-0000000000a1", "direction": "debit", "amount": "100000"},
    {"account_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6", "direction": "credit", "amount": "100000"}
  ],
  "occurred_at": "2026-07-11T10:30:00Z"
}
```

## Versioning policy

- A new **optional** field on an existing type is not a breaking change — no version bump. Old consumers ignore fields they don't know about; new consumers treat a missing field as its zero value.
- A **changed or removed** field, or a change in what an existing field means, requires a new schema version (`ledger.transaction.posted.v2`, `SchemaVersion: 2`). If consumers can't upgrade atomically, **dual-publish** both versions during the transition window, then retire the old one once all consumers have migrated.
- The `entries` array's shape (`account_id`, `direction`, `amount`) is considered stable — extending an individual `EntrySummary` follows the same optional-field rule above.

## Consuming these events

1. Import `github.com/herdifirdausss/seev/internal/ledger/events` for the types and constants — don't hand-roll a decoder.
2. Subscribe to the routing keys you care about (`ledger.transaction.posted.v1`, `ledger.transaction.reversed.v1`, `ledger.adjustment.decided.v1`).
3. Dedup by AMQP `message_id` before processing.
4. `json.Unmarshal` the message body into `events.TransactionPosted` / `events.TransactionReversed`.
5. Check `SchemaVersion` if you need to branch on schema evolution.

See [internal/ledger/events/events_test.go](../../internal/ledger/events/events_test.go) for golden examples of the exact wire bytes each type produces.
