# Service contracts

`contracts/` contains the versioned boundaries that services are allowed to
share. It is not a second home for service implementation.

```text
contracts/
├── clients/          # Typed callers for published RPC boundaries
├── events/           # Event catalog and typed event payloads
├── http/             # OpenAPI sources and generated bundles
├── proto/            # Canonical protobuf sources
├── vendorgw/         # Vendor boundary types and resilience seam
└── compatibility/    # Inventory and compatibility tests
```

Generated Go bindings are committed separately under `gen/go/`. Do not edit
generated files by hand. A contract change updates its source, regenerates
bindings where applicable, and passes the compatibility checks together.

The producing service owns evolution of its contract. Consumers may use the
published contract or a typed client, but they must not import the producer's
`internal/` packages or database models.
