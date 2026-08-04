# Payin

Payin owns the lifecycle of money entering a wallet: top-up intents, routing,
vendor confirmation correlation, intake controls, and privacy handling.

## Ownership

- Owns top-up intents, webhook/callback records, routing rules, intake state,
  and Payin retention work.
- `internal/payin/` makes money-movement decisions; `internal/repository/`
  persists Payin-owned state.
- Vendor callback authentication belongs to the Vendor service. Payin only
  consumes the normalized callback/client contract.

## Layout

- `cmd/payin/` — process composition and RPC/HTTP listener wiring.
- `internal/transport/http/` — HTTP validation, authorization, and response mapping.
- `internal/payin/` — top-up, routing, privacy, intake, and settlement use
  cases.
- `internal/payin/model/`, `internal/repository/`, and `internal/worker/` — domain
  data, persistence, and background work.
- `internal/transport/grpc/` — internal Payin RPC adapter.
- `migrations/` — Payin-owned schema changes.

## Runtime and verification

Use `services/payin/module.go` for in-process composition and generated RPC
clients for service-to-service calls. Compile with
`go test -run '^$' ./services/payin/...`.
