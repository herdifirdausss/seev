# Current Service Responsibilities — C3

> [Documentation home](../README.md) · [Reference](README.md) ·
> [Service deep dive](services.md)

This is the C3 slice of the current service inventory. The full service map is
in [services.md](services.md); this page records the notification boundary so
future work does not accidentally introduce a notification microservice.

| Component | Owns | Does not own |
|---|---|---|
| Gateway `internal/notify` | notification history, event inbox, kind policy, template lookup, planning, user settings/preferences/devices, durable email/push/digest delivery, channel controls | balances, payment decisions, Auth data, provider business state |
| Auth `internal/auth` | user identity, account status, verified email state, internal contact contract | notification history or delivery state |
| Ledger | committed money facts and the `ledger.transaction.posted.v1` event | notification copy, recipient preferences, provider calls |
| Admin BFF | browser session, CSRF boundary, downstream proxy, operator audit log | notification policy decisions and database access |
| Mailpit / SMTP adapter | local email sink and SMTP transport | notification planning or user identity |
| Mock push provider | deterministic local push acceptance, invalid-token and outage simulation | real provider credentials or financial state |

Runtime ownership is Gateway-only. External channel calls happen in workers
after the planning transaction commits, so notification failure cannot block or
reverse money movement.

The Auth contact contract is `GET /internal/v1/users/{id}/notification-contact`.
Gateway reaches it over the existing internal mTLS/token boundary and receives
only the active/verified contact decision needed for email dispatch.
