# Repository code map

> **Status: Current. Audience: contributors.** This is the short navigation
> map for the service-centric modular monolith. Follow a service README for
> ownership and invariants before changing production code.

## Start with the owning service

| Service | Entrypoint | Start here for |
|---|---|---|
| Gateway | `services/gateway/cmd/gateway` | public HTTP composition, merchant API, notifications |
| Auth | `services/auth/cmd/auth` | identity, sessions, KYC, privacy |
| Ledger | `services/ledger/cmd/ledger` | posting, balances, reconciliation, financial products |
| Payin | `services/payin/cmd/payin` | top-up intents, routing, normalized callbacks |
| Payout | `services/payout/cmd/payout` | withdrawal orchestration, vendor dispatch, recovery |
| Fraud | `services/fraud/cmd/fraud` | synchronous screening, sanctions, event enrichment |
| Admin BFF | `services/adminbff/cmd/adminbff` | operator sessions, proxying, audit |
| Assurance | `services/assurance/cmd/assurance` | read-only correlation and intake controls |
| Vendor | `services/vendor-service/cmd/vendor` | callback verification, vendor attempts, normalized delivery |

Every service root contains its `README.md`, private `internal/` code, owned
`migrations/`, and `cmd/` composition root. The physical Vendor directory is
`services/vendor-service`: Go gives a directory named `vendor` special import
semantics, so the registry keeps `vendor` as the logical service name while
using the safe physical path.

## Find a responsibility

The package names describe the responsibility rather than a generic layer:

- HTTP and gRPC decoding: `services/<service>/internal/transport/http/` or
  `services/<service>/internal/transport/grpc/`.
- Use-case orchestration and domain decisions:
  `services/<service>/internal/<domain>/` or a capability package such as
  `ledger/handle`, `ledger/recon`, or `payin/topup`.
- Consumer-owned persistence ports and implementations:
  `services/<service>/internal/repository/`.
- Background work: `services/<service>/internal/worker/`.
- Service-specific adapters: the owning service's private packages.
- Shared runtime mechanics: `internal/platform/`.
- Integration fixtures and test infrastructure: `internal/testkit/`.
- Wire contracts and compatibility evidence: `contracts/`.
- Generated Go bindings: `gen/go/`.

Representative locations:

- Auth login handler: `services/auth/internal/transport/http/http.go`.
- Payin persistence: `services/payin/internal/repository/`.
- Vendor callback verification: `services/vendor-service/internal/callback.go`.
- Ledger posting transaction boundary:
  `services/ledger/internal/ledger/handle/service.go`.

## Entry-point taxonomy

- `services/<service>/cmd/` — long-running business services.
- `tools/` — developer, CI, contract, documentation, migration, and load
  utilities.
- `operations/agents/` — long-running support agents.
- `operations/recovery/` — disaster-recovery and restore workflows.
- `tests/` — repository-wide contract, architecture, end-to-end, chaos, and
  load tests.

The root `cmd/`, root `migrations/`, and temporary `api/` trees are retired.
Architecture tests in [`tests/architecture/`](../../tests/architecture/) keep
these boundaries executable.

## Smallest useful commands

```bash
make test-service SERVICE=auth
make build-service SERVICE=ledger
make integration-service SERVICE=payin
make contracts
make docs-check
make architecture-metrics
```

For repository-wide rules, read the [Project guide](project-guide.md). For
service ownership and runtime dependencies, read the relevant service README
and the [service reference](../reference/services.md).
