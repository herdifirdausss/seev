# Notification Kind Registry

Kinds are stable semantic identifiers owned by Gateway. They are deliberately
different from Ledger transaction types because one fact can produce distinct
copies for sender and receiver.

| Kind | Ledger mapping | Recipient | Category | Deep link | Digest |
|---|---|---|---|---|---|
| `money.transfer.sent` | `transfer_p2p`, sender | `user_id` | `money_movement` | `/transactions/{id}` | yes |
| `money.transfer.received` | `transfer_p2p`, receiver | `target_user_id` | `money_movement` | `/transactions/{id}` | yes |
| `money.topup.succeeded` | `money_in` | `user_id` | `money_movement` | `/topups/{id}` | yes |
| `money.payout.succeeded` | `withdraw_settle` | `user_id` | `money_movement` | `/payouts/{id}` | yes |
| `money.payout.cancelled` | `withdraw_cancel` | `user_id` | `money_movement` | `/payouts/{id}` | yes |
| `compliance.dispute.lifecycle` | `ledger.dispute.lifecycle.v1` | `recipient_user_id` | `compliance` | `/notifications` | no |
| `system.daily_digest` | Gateway scheduler | one user | `system` | none | no |

The five money kinds are transactional/high priority. In-app is mandatory and
immediate. Email and push default to immediate when their feature flags are
enabled; a user may select email `daily_digest` where supported. The daily
digest itself is email-only and optional.

Dispute lifecycle notices are high-priority compliance notifications. They are
always planned for in-app delivery and are never moved into a digest, even if a
stale preference requests digest mode. The wire event contains bounded dispute
metadata only; evidence references and audit actors remain Ledger-owned.

Unknown transaction types are acknowledged and filtered without creating a
notification. Unknown recipient roles are rejected by the planner and recorded
as bounded planning failure evidence.
