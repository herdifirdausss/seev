# Notification Providers

C3 intentionally uses local, replaceable adapters. No paid production
provider is required by the repository.

| Channel | Adapter | Local endpoint | Contract |
|---|---|---|---|
| Email | SMTP adapter | Mailpit on `mailpit:1025` in the C3 Compose profile | multipart text/HTML, TLS mode validation, stable Message-ID |
| Push | HTTP push adapter | mock provider on `mock-push-provider:8097` | `POST /v1/messages`, delivery-id idempotency, bounded response |

Email is enabled only when `NOTIFY_EMAIL_ENABLED=true` and requires an SMTP
host/from address. Push is enabled only when `NOTIFY_PUSH_ENABLED=true` and
requires an absolute HTTP(S) provider URL. Daily digest additionally requires
`NOTIFY_DIGEST_ENABLED=true`.

The mock push provider keeps accepted messages in memory and supports
deterministic token-prefix responses for invalid, rate-limited, transient, and
server-failure cases. It never returns or logs token plaintext. Both adapters
return a bounded classification consumed by the common retry/dead state
machine; provider-specific response text is never a metric label.

Provider health is deliberately separate from financial health. Pausing or
losing SMTP/push leaves Ledger and mandatory in-app notification creation
available.
