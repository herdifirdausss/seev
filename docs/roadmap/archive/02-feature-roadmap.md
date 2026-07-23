# 02 — Feature Research: World-Class Fintech Ledger, P0–P3 Priorities

Reference practices: Modern Treasury, Stripe's internal ledger, TigerBeetle,
Formance, Square, and Uber's Gulfstream/LedgerStore. The repository-status
column shows how closely the current code is moving toward each capability.

## P0 — MVP (Phase 1): the ledger essentials

| Feature | Detail | Repository status |
|---|---|---|
| Double-entry, append-only | Σdebit = Σcredit per transaction; entries are immutable and corrections use reversals | ✅ Engine and database trigger exist; canonical migration required |
| Atomic multi-leg transactions | Movement and fee in one database transaction with one outbox event | ✅ Inline-fee design exists in the processors |
| Idempotency key (+ scope) | Safe retries; duplicates return the first result | ✅ Engine exists; the unique index needs `COALESCE` (D4) |
| Concurrency safety | Deterministic `FOR UPDATE` order, retry with jitter, and non-negative database balances | ✅ Engine exists; ⬜ concurrent-load test required |
| Chart of accounts | User accounts (`cash/hold/pending/frozen/pocket`) and system accounts (`settlement/fee/escrow/chargeback/confiscated/adjustment`), qualified by gateway/currency | ⚠️ Constants exist; ⬜ provisioning and `AccountRepository` implementation required |
| Available, held, and pending balances | Separate accounts rather than flags | ✅ Account-type design exists |
| Monetary precision | `BIGINT` minor units plus `decimal.Decimal` | ✅ |
| Core transaction types | `money_in`, `money_out`, `transfer_p2p` | ✅ Processors exist; ⬜ not yet callable from outside the module |
| HTTP API | Post a transaction, get a balance, get a transaction, and list entries with a cursor | ⬜ Not implemented |
| Transactional outbox → broker | Write the event in the same transaction as the posting; relay it to RabbitMQ | ⚠️ Insert exists; ⬜ relay worker required |
| Invariant verification (trial balance) | Daily job: Σledger = 0 per transaction; stored balance = Σentries per account | ⚠️ SQL functions exist in draft form; ⬜ job required |
| Audit trail | `created_by`, `error_message`, and failed headers remain committed | ✅ Partially implemented |

## P1 — Phase 2: immediately after MVP

| Feature | Detail | Repository status |
|---|---|---|
| Hold / authorize–capture | Initiate → settle/cancel; foundation for escrow and card authorization | ✅ Withdrawal and escrow lifecycle processors exist |
| Reversal / refund / chargeback | Always reference the original transaction; partial refunds can follow | ✅ Processors exist; ⬜ test the reversal-of-reversal guard |
| Fee engine | Inline fees plus fee rules by transaction type and gateway | ⚠️ Mechanism exists; rule engine required |
| Freeze / confiscate | Compliance flow with reason and audit trail | ✅ Processors exist |
| External reconciliation | Compare the ledger with gateway/bank settlement reports; use suspense accounts for differences | ⬜ |
| Daily balance snapshots | Closing balance per account per day; as-of queries without a full scan | ⬜ |
| Versioned event contracts | Stable event payload schema such as `ledger.transaction.posted.v1` for other modules | ⬜ |
| Statements / exports | Account statements and CSV export | ⬜ |

## P2 — Phase 3: scale and business requirements

| Feature | Detail |
|---|---|
| Multi-currency and FX | Post across currencies through conversion accounts; store the exchange rate as a transaction fact |
| Limits and velocity | Per-transaction, daily, and monthly limits in the policy layer, not in the ledger |
| Maker-checker | `adjustment_*` transactions require approval by a second person before posting |
| Scheduled and batch posting | Scheduled transactions and bulk disbursement using `pkg/scheduler` |
| Hot-account mitigation | System accounts are already sharded by gateway; next steps include entry batching or asynchronous balances for extremely hot accounts |
| Partitioning and archival | Monthly `ledger_entries` partitions, retention, and archival |
| Pagination and read replicas | Move statement and balance-history reads to a replica |

## P3 — World-class capabilities and full compliance

| Feature | Detail |
|---|---|
| AML / fraud hooks | Pre-posting hook points for sanctions and anomalous velocity, with external integrations |
| Regulatory reporting | Fund positions for regulators such as BI/OJK and automated periodic reports |
| Interest / yield accrual | Daily savings-product interest and automatic accrual postings |
| Point-in-time rebuild | Replay all state from entries and run a disaster-recovery drill |
| Multi-region / HA | Failover, RPO/RTO targets, and an exactly-once outbox-semantics review |

## Deliberately outside the ledger scope

- Per-user rate and daily limits belong in the API or policy layer, as already
  documented in `processors.go`.
- FX conversion execution belongs in the orchestration layer
  (`money_out` + `money_in` through an FX service).
- User management, KYC, and login belong in a separate `auth` module.
- Email and push notifications belong in a `notification` module that consumes
  outbox events.
