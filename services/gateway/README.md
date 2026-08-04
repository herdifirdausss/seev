# Gateway

Gateway is the public edge. It authenticates merchant API requests, owns the
merchant B2B surface, coordinates public request flow, and consumes the
notification outbox without owning Ledger, Payin, or Payout decisions.

## Ownership

- Owns merchants, API keys, tenant lifecycle, quotas, idempotency, webhook
  endpoints/deliveries, notification records, and Gateway retention work.
- Calls domain services through their public facades or generated clients.
- Never imports another service's `internal` package.

## Layout

- `services/gateway/cmd/gateway/` — the public and internal listener composition root.
- `internal/transport/http/` — public routing and request adapters.
- `internal/merchant/` — merchant API capabilities, repositories, auth,
  quotas, idempotency, and webhook delivery.
- `internal/notification/` — notification service, channel adapters, registry,
  templates, repository, and workers.
- `migrations/` — Gateway-owned schema changes.

## Runtime and verification

Run `go run ./services/gateway/cmd/gateway` locally. Compile with
`go test -run '^$' ./services/gateway/...`; verify public routes with the
Gateway HTTP contract tests.
