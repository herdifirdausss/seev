# Repository-private code

The root `internal/` directory is intentionally small. It contains only
cross-service platform mechanics and the repository-wide integration test kit.
Business ownership lives under `services/<service>/`.

```text
internal/
├── platform/
│   ├── config/
│   ├── database/
│   ├── cache/
│   ├── lifecycle/
│   ├── messaging/
│   ├── migration/
│   ├── money/
│   ├── observability/
│   ├── resilience/
│   ├── scheduling/
│   ├── security/
│   └── transport/
└── testkit/                         # Test-only integration fixtures
```

Use the narrowest service package for business behavior:

```text
services/<service>/internal/
├── <domain>/                        # Use-case composition and decisions
├── repository/                      # Service-owned persistence
├── transport/http/                  # HTTP decoding and response mapping
├── transport/grpc/                  # gRPC transport, when present
├── worker/                          # Background work, when present
└── adapter/                         # External-system implementations
```

Large domain roots may include a local `README.md` capability map. The Auth,
Ledger, Payin, and Payout roots use this to make a dense but cohesive package
readable without inventing artificial layers.

Do not create a root-level `application/`, `common/`, `utils/`, `service/`, or `pkg/`
bucket. Contracts belong under `contracts/`, generated code under `gen/go/`,
operator workflows under `operations/`, and developer/load tools under
`tools/`.

For ownership and import rules, see the
[architecture registry](../tests/architecture/registry.go),
[boundary test](../boundary_test.go), and
[project guide](../docs/development/project-guide.md).
