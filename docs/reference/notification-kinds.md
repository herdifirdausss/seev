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
| `system.daily_digest` | Gateway scheduler | one user | `system` | none | no |

The five money kinds are transactional/high priority. In-app is mandatory and
immediate. Email and push default to immediate when their feature flags are
enabled; a user may select email `daily_digest` where supported. The daily
digest itself is email-only and optional.

Unknown transaction types are acknowledged and filtered without creating a
notification. Unknown recipient roles are rejected by the planner and recorded
as bounded planning failure evidence.
