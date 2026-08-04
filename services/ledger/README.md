# Ledger

Ledger is the double-entry source of truth. Every money-moving use case must
produce balanced, auditable postings here; callers cannot mutate balances by
writing tables directly.

## Ownership

- Owns accounts, balances, entries, transactions, outbox events, fee policy,
  scheduled movements, reconciliation, and ledger retention work.
- `internal/repository/` is the persistence boundary; `internal/processors/`
  contains balanced posting strategies.
- `policy/` is the explicit ledger-policy API used by the service and offline
  recovery tooling.

## Layout

- `cmd/ledger/` — process composition and listener wiring.
- `internal/ledger/` — transfer, closure, command, disbursement, dispute,
  FX, interest, reconciliation, and scheduling use cases.
- `internal/ledger/model/`, `internal/repository/`, and `internal/worker/` — domain
  data, persistence, and background processing.
- `internal/transport/` — HTTP and gRPC adapters.
- `policy/` — transaction-limit policy engine, repository, and admin handler.
- `migrations/` — Ledger-owned schema changes.

## Runtime and verification

The public facade in `services/ledger/module.go` and generated clients are the
supported synchronous boundaries. Ledger publishes event shapes under
`contracts/events/ledger`. Compile with `go test -run '^$' ./services/ledger/...`.
