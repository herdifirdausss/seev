# Assurance

Assurance is the read-only cross-service control plane. It correlates Payin,
Payout, and Ledger evidence, persists findings in its own database, and emits
operator alerts. A finding never changes money state automatically.

## Ownership

- Owns assurance runs, findings, cursors, retention holds, and alert metadata.
- Reads other services through authenticated clients or evidence contracts;
  it never writes another service's tables.
- `rules/` is the reusable invariant evaluator used by the service and the
  offline recovery verifier.

## Layout

- `cmd/assurance/` — process composition.
- `internal/assurance/` — correlation, finding persistence, retention, and HTTP
  use cases.
- `rules/` — pure finding rules and evidence DTOs.
- `migrations/` — Assurance-owned schema changes.

## Runtime and verification

The service provides internal assurance APIs and scheduled verification work.
Run `go test -run '^$' ./services/assurance/...` for a dependency/compile
check; run the tagged integration tests when Postgres and dependent services
are available.
