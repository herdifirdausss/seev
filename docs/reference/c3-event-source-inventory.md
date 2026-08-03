# C3 Event Source Inventory

| Source | Routing key | Consumer | Mapping |
|---|---|---|---|
| Ledger outbox | `ledger.transaction.posted.v1` | Gateway `ledger.events.notifications` | `money_in` → top-up; `transfer_p2p` → sender/receiver; `withdraw_settle` → payout success; `withdraw_cancel` → payout cancelled |

The payload contains accounting facts and IDs, not user-facing prose. Gateway
validates the event schema, filters unsupported transaction types, stores only a
hash in the event inbox, and derives bounded template context. There is no
notification dependency in the money-posting transaction.
