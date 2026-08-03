# C2 source inventory

This is the review baseline derived from the service migrations at commit
`ff21bf1` in the implementation worktree. Runtime database sizes, PostgreSQL
image digest, logical-WAL settings, and live migration tables are intentionally
marked pending until the entry gate is executed.

| Service | Database | Migration head in repository | Initial tables | Owner |
| --- | --- | ---: | --- | --- |
| Ledger | `seev_ledger` | `000038` | accounts, account_balances, ledger_transactions, ledger_entries, fee_quotes | LedgerService |
| Payin | `seev_payin` | `000016` | payin_topup_intents, payin_webhook_events | PayinService |
| Payout | `seev_payout` | `000016` | payout_requests, payout_vendor_calls | PayoutService |

## Captured fields

The exact include/exclude and transformation decision for every selected field
is in `analytics/contracts/sources.yaml`. The connector files repeat those
allowlists because a connector must fail closed even if the manifest is later
edited incorrectly.

Excluded from C2 v1 are authentication/KYC/session tables, raw callback bodies,
raw vendor request/response data, payout destinations, credentials, raw error
messages, idempotency secrets, and operator identity fields.

## Database prerequisites

The application Compose PostgreSQL command now enables logical WAL, bounds
replication slots/senders, and caps retained slot WAL. The reproducible source
setup script creates one `LOGIN REPLICATION` role, explicit publication, and
stable slot name per selected source. It grants only `CONNECT`, schema `USAGE`,
and `SELECT` on the reviewed tables. Normal application shutdown does not drop
slots; slot deletion is an explicitly confirmed source-protection action.

## Known source gaps

Payin's current schema has a deterministic webhook-to-intent reference but no
persisted Payin-to-Ledger transaction identifier. C2 leaves that field null and
reports the gap; it does not infer a relationship from amount/time/status. A
future additive Payin owner migration may close the gap.
