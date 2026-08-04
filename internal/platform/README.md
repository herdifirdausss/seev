# Shared platform

`internal/platform/` contains repository-private infrastructure used by more
than one service. It is organized by capability so a contributor can choose a
specific dependency instead of searching a generic shared folder.

```text
internal/platform/
├── config/                         # Runtime configuration and secret loading
├── database/                       # PostgreSQL lifecycle and database primitives
│   ├── identifiers/                # Repository-wide ID generation
│   └── nulls/                      # SQL nullable-value arguments
├── cache/                          # Redis cache, counters, and rate limiting
├── errors/                         # Truly platform-level database error helpers
├── lifecycle/                      # Privacy, retention, and object-outbox mechanics
├── messaging/                      # RabbitMQ broker, publisher, and consumer primitives
├── migration/                      # Migration execution mechanics
├── money/currency/                 # Currency registry and minor-unit rules
├── observability/                  # Logging, metrics, and tracing setup
├── resilience/                     # Alerting and outbound proxy controls
├── scheduling/                     # Distributed scheduled-job runner
├── security/                       # Crypto, TLS identity, and HTTP security middleware
└── transport/                      # HTTP/gRPC bootstrap and response helpers
```

Platform code may depend on the standard library, third-party infrastructure
libraries, and generated contracts where necessary. It must not import
`services/` or encode a service's business vocabulary, table names, event
names, or workflow states. If code serves one service, keep it under that
service until reuse is proven.

`internal/testkit/` is separate and test-only. It may assemble service facades
to provide repository-wide integration fixtures, but it is never a production
runtime dependency.

For the admission rules and dependency direction, see the
[architecture registry](../../tests/architecture/registry.go),
[boundary test](../../boundary_test.go), and
[shared infrastructure guide](../../docs/reference/shared-packages.md).
