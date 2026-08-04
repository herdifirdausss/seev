# Notification Platform Reference

> [Documentation home](../README.md) · [Architecture](../architecture/notification-platform.md)

Gateway owns the notification platform. A committed Ledger event enters the
RabbitMQ queue `ledger.events.notifications`, is validated and mapped by
`services/gateway/internal/notification`, and is atomically recorded with its user-facing plan in
Gateway PostgreSQL. RabbitMQ acknowledgement happens only after that durable
plan succeeds.

The runtime path is:

```text
Ledger outbox
  -> ledger.transaction.posted.v1
  -> ledger.events.notifications
  -> Gateway event inbox + kind/recipient mapping
  -> in-app row + durable email/push/digest rows
  -> leased workers
  -> local SMTP/Mailpit or mock push provider
```

The event is at-least-once. The event inbox deduplicates the source event and
`(event_id, user_id, kind)` deduplicates the logical notification. In-app is a
logical exactly-once result; email and push are at-least-once and may duplicate
across a provider-acceptance/worker-crash boundary.

## Public surface

All routes below are under `/api/v1` and require the authenticated user's JWT.
Ownership is enforced again in SQL with the user ID from the token.

| Method | Route | Purpose |
|---|---|---|
| GET | `/notifications` | keyset list with `limit`, `before`, `unread`, `category`, and `kind` filters |
| GET | `/notifications/{id}` | owner-scoped detail |
| GET | `/notifications/unread-count` | unread count |
| POST | `/notifications/{id}/read` | mark one row read |
| POST | `/notifications/read-all` | mark all, optionally before a timestamp |
| GET/PUT | `/notification-settings` | locale, timezone, quiet hours, digest time, optimistic version |
| GET/PUT | `/notification-preferences` | category/channel delivery modes |
| GET/POST | `/notification-devices` | list or register encrypted push endpoints |
| DELETE | `/notification-devices/{id}` | revoke one endpoint |

In-app responses preserve the legacy `type`, `title`, `body`, `read_at`, and
`created_at` fields while adding `kind`, `category`, `priority`, and `deep_link`.

## Gateway-owned data

The C3 migration adds `notif_event_inbox`, governed template catalog/version
tables, `notif_user_settings`, `notif_preferences`, `notif_device_endpoints`,
`notif_deliveries`, `notif_delivery_attempts`, `notif_digest_windows`,
`notif_digest_items`, and `notif_channel_controls`. Existing
`notif_notifications` rows remain readable during the additive cutover.

New notification rows do not persist a raw AMQP event: they store a bounded
typed render context and `{}` in the legacy payload column. Recipient email and
device tokens are encrypted at rest and are never returned by public APIs.

See [notification kinds](notification-kinds.md),
[delivery](notification-delivery.md), [preferences](notification-preferences.md),
and the [privacy inventory](c3-privacy-inventory.md) for operational detail.
