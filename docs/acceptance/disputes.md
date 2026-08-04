# Chargeback dispute acceptance

| Field | Value |
|---|---|
| Owner | Ledger/Compliance |
| Code | `services/ledger/internal/ledger/dispute` |
| Schema | `000035_chargeback_disputes`, `000037_chargeback_dispute_audit_trail`, amount/deadline migrations |

- [x] Opening requires an existing original transaction and matching currency.
- [x] Amount is positive and cannot exceed the original transaction amount.
- [x] Evidence submission is allowed only while open and before the deadline.
- [x] Won/lost/expired are terminal and require actor/reason/audit records.
- [x] Concurrent transitions use row locks plus status-guarded updates.
- [x] Deadline expiry is worker-driven and idempotent.
- [x] Original ledger history is immutable; compensation is a new posting.
- [x] Lifecycle notifications are emitted through the transactional outbox, consumed by Gateway, delivered in-app, and deduplicated on logical event ID; live Postgres/RabbitMQ evidence is in `services/gateway/internal/notification/inbox/notify_integration_test.go` (`TestNotify_DisputeLifecycle_RealStack_TransactionalOutboxToInAppDelivery`).
- [x] Notification retention, legal-hold exclusion, dry-run parity, and direct-delete prevention are covered by the real-Postgres tests in `services/gateway/internal/notification/inbox/retention_integration_test.go`.

Verification:

```sh
go test ./services/ledger/internal/ledger/dispute ./services/ledger/internal/repository
go test -tags integration ./services/ledger -run TestSchemaContract_ChargebackDispute_FullLifecycle -count=1
go test -tags integration ./services/gateway/internal/notification -run 'TestNotify_DisputeLifecycle_RealStack_TransactionalOutboxToInAppDelivery|TestRetention_Notifications' -count=1
make capability-e2e
```
