# Admin BFF

Admin BFF is the operator-facing backend-for-frontend. It authenticates and
authorizes operator sessions, serves the embedded console, and proxies typed
administrative operations to the owning services.

## Ownership

- Owns operator sessions, CSRF state, audit records, and its own retention
  metadata.
- Does not read or write another service's database.
- Uses service clients and published facades for downstream operations.

## Layout

- `cmd/adminbff/` — process composition and listener setup.
- `internal/admin/` — session, proxy, audit, and retention use cases.
- `internal/client/` — typed downstream clients.
- `internal/web/` — embedded console templates and assets.
- `migrations/` — Admin BFF-owned schema changes.

## Runtime and verification

The service exposes the operator HTTP surface and calls Ledger, Auth, Payin,
Payout, Assurance, and Gateway through explicit clients. Start with
`go run ./services/adminbff/cmd/adminbff`; compile it with
`go test -run '^$' ./services/adminbff/...`.
