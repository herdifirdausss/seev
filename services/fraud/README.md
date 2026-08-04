# Fraud

Fraud owns synchronous screening and asynchronous risk enrichment. It manages
rule modes, velocity state, sanctions matching, and the fail-closed behavior
used when risk evidence is unavailable.

## Ownership

- Owns screening events, rule modes, sanctions records, velocity state, and
  Fraud retention work.
- `repository/` owns all Fraud persistence interfaces and implementations.
- `rules/` contains the named screening rules; `sanctions/` only normalizes
  and matches sanctions data.

## Layout

- `cmd/fraud/` — service composition; `services/fraud/cmd/sanctions-loader/` — the
  Fraud-owned offline sanctions import utility.
- `internal/fraud/` — screening orchestration, consumers, spill, and HTTP
  use cases.
- `internal/repository/` and `internal/fraud/model/` — persistence boundary and
  domain data.
- `internal/transport/grpc/` — internal screening RPC adapter.
- `rules/` — reusable rule implementations and velocity key builders.
- `migrations/` — Fraud-owned schema changes.

## Runtime and verification

The public facade in `services/fraud/module.go` is the only cross-service
entrypoint. Compile with `go test -run '^$' ./services/fraud/...`; exercise
the fail-closed integration paths with the tagged test environment.
