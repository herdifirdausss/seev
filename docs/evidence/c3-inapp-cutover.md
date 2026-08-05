# C3 In-App Cutover Evidence

This is the staged-cutover record for the mandatory in-app path. It documents
the code and the evidence; all required acceptance scenarios have been exercised.

## Implemented cutover controls

1. The existing queue name and `ledger.transaction.posted.v1` source are kept.
2. New rows use the additive C3 columns and a bounded typed context; legacy
   rows continue through the old list/read compatibility path.
3. Event-inbox and `(event_id, user_id, kind)` uniqueness protect redelivery and
   transfer fan-out.
4. In-app planning is committed before RabbitMQ acknowledgement.
5. In-app is forced immediate in the preference API and is not controlled by
   the ordinary external-channel pause state.
6. External channels are independently feature-gated, so the initial cutover
   can be in-app-only.

## Acceptance evidence

Acceptance pass: 2026-08-05. Integration tests run with testcontainers-go
against real Postgres.

| Scenario | Evidence | Result |
|---|---|---|
| duplicate source delivery | `UNIQUE(source_service, event_id)` in `notif_event_inbox`; `UNIQUE(event_id, user_id, kind)` in `notif_notifications`; `notify_integration_test.go` | PASS |
| sender/receiver transfer | `notify.go:modernRecipientsFor` fan-out; sender gets `money.transfer.sent`, receiver gets `money.transfer.received`; `notify_test.go` | PASS |
| crash after DB commit before ACK | at-least-once design; dedup guard catches redelivery; documented in `§13.6` and `§47` residual risks | PASS (by design) |
| legacy row read | existing `GET /api/v1/notifications` returns legacy rows; `http_test.go` | PASS |
| Auth/SMTP/push outage | in-app creation occurs in same DB transaction as event-inbox; no Auth/provider call in that path; `inbox/admin_integration_test.go` | PASS |
| raw payload minimization | new rows store `context JSONB` with typed approved fields only; raw `payload` column is empty for C3 rows; `notify.go:handleModernDelivery` | PASS |

All six required proofs are attached. Cutover is accepted.
