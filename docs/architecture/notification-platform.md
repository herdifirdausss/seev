# C3 Notification Platform Architecture

> [Architecture reference](../reference/architecture.md) ·
> [Notification reference](../reference/notifications.md) ·
> [Plan 59](../roadmap/active/59-c3-multi-channel-notifications.md)

## Boundary

Notifications stay inside Gateway as `internal/notify`. The module owns the
user-facing projection and durable delivery state; it does not become a new
business service and it never participates in a money-posting transaction.
Ledger remains the source of truth for facts. Auth remains the source of truth
for identity and verified contact state. Admin BFF remains the browser and
operator-audit boundary.

## Flow

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

The planner uses a typed registry and restricted renderer. One transfer creates
separate sender and receiver copies. In-app is committed as a logical
exactly-once projection using database uniqueness. External delivery is
at-least-once: leases, attempt records, provider idempotency keys, and stable
rendered snapshots limit but cannot eliminate duplicate external messages.

## Isolation and failure behavior

- Auth contact resolution is used only by the email worker. Auth outage leaves
  event ingestion and in-app delivery available; email remains pending/retryable.
- SMTP and push providers are feature-gated and independently pausable.
- Quiet hours and preferences are checked at planning time and again at
  dispatch time.
- Template changes do not mutate existing delivery snapshots. Missing external
  templates become blocked work; mandatory in-app template failure is fail-closed.
- Retention and privacy functions run through Gateway's bounded retention
  interface. Closure removes live recipient configuration and pseudonymizes
  retained operational history.

## Deployment shape

The default local deployment keeps email and push disabled while in-app remains
enabled. The optional C3 Compose profile provides Mailpit and the deterministic
mock push provider. Production deployments must supply TLS SMTP settings,
provider credentials, and a dedicated notification token-fingerprint key; the
repository does not require a paid provider.
