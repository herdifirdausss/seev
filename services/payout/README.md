# Payout

Payout owns the lifecycle of money leaving a wallet: withdrawal intents,
routing, vendor commands, failover, intake controls, and safe resumption.

## Ownership

- Owns payout requests, vendor commands/calls, routing rules, intake state,
  and Payout retention work.
- `internal/payout/` decides when a payout may progress or settle;
  `internal/repository/` is the only persistence boundary.
- Vendor transport is composed through the Vendor service contract; Payout
  retains the business decision and ledger invariant.

## Layout

- `cmd/payout/` — process composition and RPC/HTTP listener wiring.
- `internal/transport/http/` — HTTP validation, authorization, and response mapping.
- `internal/payout/` — withdrawal, orchestration, routing, relay, privacy,
  and intake use cases.
- `internal/payout/model/`, `internal/repository/`, and `internal/worker/` — domain
  data, persistence, and retry/relay work.
- `internal/transport/grpc/` — internal Payout RPC adapter.
- `migrations/` — Payout-owned schema changes.

## Runtime and verification

Use `services/payout/module.go` for in-process composition and generated RPC
clients for service-to-service calls. Compile with
`go test -run '^$' ./services/payout/...`.
