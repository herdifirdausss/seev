# Current Service Responsibilities

> [Documentation home](../README.md) · [Reference](README.md) ·
> [Service deep dive](services.md)

This page records current cross-cutting ownership decisions. The full service
map is in [services.md](services.md); feature-specific plans remain in the
[roadmap](../roadmap/README.md).

| Component | Owns | Does not own |
|---|---|---|
| Gateway `internal/notify` | notification history, event inbox, kind policy, template lookup, planning, user settings/preferences/devices, durable email/push/digest delivery, channel controls | balances, payment decisions, Auth data, provider business state |
| Auth `internal/auth` | user identity, account status, verified email state, internal contact contract | notification history or delivery state |
| Ledger | committed money facts, currency/FX, savings and schedule state, migration controls, and the `ledger.transaction.posted.v1` event | notification copy, recipient preferences, provider calls |
| Admin BFF | browser session, CSRF boundary, typed downstream proxy, operator audit log, and FX/migration consoles | notification policy decisions and direct database access |
| Analytics stack | optional read-only CDC/OLAP projection and reconciliation evidence | OLTP writes, money authority, or transactional dependencies |
| Mailpit / SMTP adapter | local email sink and SMTP transport | notification planning or user identity |
| Mock push provider | deterministic local push acceptance, invalid-token and outage simulation | real provider credentials or financial state |

Runtime ownership is Gateway-only. External channel calls happen in workers
after the planning transaction commits, so notification failure cannot block or
reverse money movement.

The Auth contact contract is `GET /internal/v1/users/{id}/notification-contact`.
Gateway reaches it over the existing internal mTLS/token boundary and receives
only the active/verified contact decision needed for email dispatch.
