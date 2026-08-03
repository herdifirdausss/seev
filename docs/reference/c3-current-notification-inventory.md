# C3 Current Notification Inventory

> Entry-gate artifact for [Plan 59](../roadmap/active/59-c3-multi-channel-notifications.md).

| Existing surface | Owner | C3 treatment |
|---|---|---|
| `GET /api/v1/notifications` | Gateway notify | preserved; filters and additive response fields are supported |
| `POST /api/v1/notifications/{id}/read` | Gateway notify | preserved; ownership remains SQL-scoped |
| `ledger.transaction.posted.v1` | Ledger | sole initial source event; consumed at least once |
| `notif_notifications` | Gateway | additive C3 columns; old rows remain readable |
| `ledger.events.notifications` | RabbitMQ | existing notification queue name retained |

New surfaces and workers are listed in [notifications.md](notifications.md) and
the [current service slice](current-services.md). Acceptance execution remains
separate from this code-only inventory.
