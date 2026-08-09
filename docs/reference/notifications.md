# Notification Platform Reference

> [Documentation home](../README.md) · [Reference](README.md)

Gateway owns the notification platform (`services/gateway/internal/notification`). A committed
Ledger event enters `ledger.events.notifications`, is validated and mapped, and is atomically
recorded with its user-facing plan in Gateway PostgreSQL. No notification code participates in
a money-posting transaction.

## Architecture

```text
Ledger commit
  -> outbox relay
  -> RabbitMQ ledger.events.notifications
  -> Gateway validate/filter
  -> event inbox + logical notification + delivery plan (one DB transaction)
  -> ACK
  -> email/push/digest lease worker
  -> provider call outside transaction
  -> attempt + delivered/retry/dead state
```

The planner uses a typed registry and restricted renderer. One transfer creates separate sender
and receiver copies. In-app is committed as a logical exactly-once projection using database
uniqueness. External delivery is at-least-once: leases, attempt records, provider idempotency
keys, and stable rendered snapshots limit but cannot eliminate duplicate external messages.

**Isolation**: Auth contact resolution is used only by the email worker. Auth outage leaves
event ingestion and in-app delivery available; email remains pending/retryable. SMTP and push
providers are feature-gated and independently pausable. Quiet hours and preferences are checked
at planning time and again at dispatch time. Template changes do not mutate existing delivery
snapshots.

## Event sources

| Source | Routing key | Consumer queue | Mapping |
|---|---|---|---|
| Ledger outbox | `ledger.transaction.posted.v1` | `ledger.events.notifications` | `money_in` → top-up; `transfer_p2p` → sender/receiver; `withdraw_settle` → payout success; `withdraw_cancel` → payout cancelled |
| Ledger outbox | `ledger.dispute.lifecycle.v1` | `ledger.events.notifications` | Dispute open/evidence/terminal state → compliance notification |

The payload contains accounting facts and IDs, not user-facing prose. Gateway validates the
event schema, filters unsupported transaction types, and stores only a hash in the event inbox.

## Notification kinds

| Kind | Ledger mapping | Recipient | Category | Deep link | Digest eligible |
|---|---|---|---|---|---|
| `money.transfer.sent` | `transfer_p2p`, sender | `user_id` | `money_movement` | `/transactions/{id}` | yes |
| `money.transfer.received` | `transfer_p2p`, receiver | `target_user_id` | `money_movement` | `/transactions/{id}` | yes |
| `money.topup.succeeded` | `money_in` | `user_id` | `money_movement` | `/topups/{id}` | yes |
| `money.payout.succeeded` | `withdraw_settle` | `user_id` | `money_movement` | `/payouts/{id}` | yes |
| `money.payout.cancelled` | `withdraw_cancel` | `user_id` | `money_movement` | `/payouts/{id}` | yes |
| `compliance.dispute.lifecycle` | `ledger.dispute.lifecycle.v1` | `recipient_user_id` | `compliance` | `/notifications` | no |
| `system.daily_digest` | Gateway scheduler | one user | `system` | none | no |

Dispute lifecycle notices are always in-app and are never moved into a digest. Unknown
transaction types are acknowledged and filtered without creating a notification.

## Public API surface

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

## Preferences and settings

Settings (`/api/v1/notification-settings`) accept `locale` (`en-US` or `id-ID`), IANA `timezone`,
optional quiet hours (`HH:MM` start/end, cross-midnight supported), and `daily_digest_hour`.

Preferences select a `category`, `channel`, and `mode`: `immediate`, `daily_digest`, or
`disabled`. `daily_digest` is valid only for email and digest-eligible kinds. In-app is forced to
`immediate`; the API rejects attempts to disable or digest it.

## Push devices

`POST /api/v1/notification-devices` accepts `android`, `ios`, `web`, or `test` and a bounded
token. Gateway stores the token only as encrypted ciphertext with a key version, HMAC fingerprint,
and four-character suffix. The plaintext token is never returned, logged, put in metrics, or
exported. A cross-user fingerprint collision is rejected; same-user re-registration re-seals the
token.

## Providers

| Channel | Adapter | Local endpoint | Key contract |
|---|---|---|---|
| Email | SMTP adapter | Mailpit on `mailpit:1025` (C3 Compose profile) | multipart text/HTML, TLS mode validation, stable Message-ID |
| Push | HTTP adapter | mock provider on `mock-push-provider:8097` | `POST /v1/messages`, delivery-id idempotency, bounded response |

Email requires `NOTIFY_EMAIL_ENABLED=true`; push requires `NOTIFY_PUSH_ENABLED=true`; digest
additionally requires `NOTIFY_DIGEST_ENABLED=true`. Provider health is deliberately separate
from financial health: pausing or losing SMTP/push leaves Ledger and mandatory in-app creation
available.

## Templates

Templates are versioned Gateway data, selected by kind, channel, and locale. Each delivery stores
the rendered subject/title/body, content hash, locale, and template version at planning time;
retry never re-renders against a newer version.

**Template context** accepts only: canonical minor-unit amount, currency, formatted display
amount, transaction ID, timestamp, generated notification ID, and internal deep link. Raw events,
arbitrary maps, credentials, recipient addresses, and provider fields are not template variables.

**Lifecycle**: `draft → pending_approval → active → retired`; `pending_approval → rejected`.
A checker cannot be the same actor as the maker. Active versions are immutable; the database
enforces one active version per kind/channel/locale.

## Delivery

In-app delivery is inserted as `delivered` in the same Gateway transaction as the logical
notification. Email and push are durable rows processed by independent leased workers.

| State | Meaning |
|---|---|
| `pending_recipient` | email needs a verified Auth contact resolved and encrypted |
| `scheduled` | eligible for a provider attempt |
| `processing` | leased by one worker |
| `retry_wait` | transient failure with a bounded next-attempt time |
| `delivered` | provider accepted the message |
| `suppressed` | preference, quiet-hour, inactive-device, or unverified-contact guard |
| `blocked` | operator action is needed (usually a missing/invalid template) |
| `dead` | permanent failure or exhausted retry schedule |

Claims use `FOR UPDATE SKIP LOCKED`, an owner, and an expiry lease. Email uses a deterministic
Message-ID and push uses the delivery ID as the provider idempotency key, but neither channel
promises exactly-once external delivery — a provider acceptance followed by a process failure
can produce a duplicate on retry.

### Digest

Digest windows are unique by user, email channel, local window date, and timezone. Empty windows
are suppressed. A bounded number of items is rendered with a `more_count`.

## Gateway-owned data

| Table | Purpose |
|---|---|
| `notif_event_inbox` | deduplication and source event record |
| `notif_notifications` | logical user-facing notification (additive over legacy `notif_notifications`) |
| `notif_user_settings` | locale, timezone, quiet hours, digest time |
| `notif_preferences` | per category/channel mode |
| `notif_device_endpoints` | encrypted push endpoint tokens |
| `notif_deliveries` | durable email/push/digest delivery rows |
| `notif_delivery_attempts` | per-attempt result record |
| `notif_digest_windows` | email digest collection windows |
| `notif_digest_items` | notifications belonging to a digest window |
| `notif_channel_controls` | operator channel pause/drain controls |
| `notif_template_catalog` / `notif_template_versions` | versioned template management |

## Privacy

| Data | Storage | Treatment |
|---|---|---|
| Notification copy and typed context | `notif_notifications` | user-scoped reads; raw source payload is `{}` for new rows; bounded retention |
| Email recipient | `notif_deliveries.recipient_ciphertext` | cryptox envelope; decrypted only immediately before SMTP; redacted after operational window |
| Push token | `notif_device_endpoints.token_ciphertext` | cryptox envelope; public API exposes only suffix/status |
| Delivery evidence | `notif_deliveries`, `notif_delivery_attempts` | status, hashes, bounded codes/excerpts; no recipient plaintext |
| Preferences/settings | `notif_preferences`, `notif_user_settings` | closure deletes current subject state |

Account closure deletes settings, preferences, and device endpoints, then pseudonymizes retained
notification/delivery/digest ownership with a stable surrogate.

## Operator recovery

Admin BFF exposes sanitized delivery inspection, maker/checker template changes, channel
`running`/`paused`/`drain_only` controls, and reason-required replay for dead/blocked rows.
See `docs/operations/runbooks/` for provider outage, duplicate delivery, dead delivery, and
channel pause runbooks.

## Deployment shape

The default local deployment keeps email and push disabled while in-app remains enabled.
The optional C3 Compose profile provides Mailpit and the deterministic mock push provider.
Production deployments must supply TLS SMTP settings, provider credentials, and a dedicated
notification token-fingerprint key; the repository does not require a paid provider.
