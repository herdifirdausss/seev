# Internal service-call matrix

All service-to-service HTTP and gRPC calls use a service leaf certificate and
an allowlisted peer identity. Timeouts and retry/failure semantics are part of
the machine-readable [dependency contract](../../deploy/inventory/dependencies.yaml).

| Caller | Target | Transport | Purpose | Readiness | Failure posture |
|---|---|---|---|---|---|
| Gateway | Ledger | HTTPS 8091, gRPC 9091 | user ledger and posting | critical | bounded, idempotent-only retry |
| Gateway | Payin / Payout | gRPC 9092 / 9093 | money orchestration | critical | no double-submit; return dependency error |
| Auth | Ledger, Payin, Payout, Fraud, Gateway | mTLS HTTP/gRPC | identity and privacy composition | critical | fail closed |
| Payin | Ledger, Fraud, Vendor | mTLS gRPC | topup, risk, provider boundary | critical | no completion without safe dependency result |
| Payout | Ledger, Fraud, Vendor | mTLS gRPC | payout, risk, provider boundary | critical | pending/recovery and idempotent status |
| Vendor | Payin / Payout | mTLS gRPC | normalized callbacks | callback | retain and retry callback |
| Admin BFF | Auth, Ledger, Payin, Payout, Fraud, Gateway | mTLS HTTPS | typed operator proxy | admin | safe operator error |
| Assurance | Ledger, Payin, Payout | mTLS gRPC | independent correlation | audit | pending/alert; no balance edits |
| Application services | PostgreSQL | TCP 5432 | owner database only | critical | readiness failure |
| Application services | Redis | TCP 6379 | rate limits, locks, velocity, cache | critical/feature | feature-specific degradation; money paths fail closed |
| Ledger, Fraud, Gateway | RabbitMQ | AMQP 5672 | durable event transport | critical | outbox, confirms, retry/DLQ |
| Vendor | Squid | HTTP CONNECT 3128 | certified external provider egress | deferred | direct path denied |
| All services | DNS / telemetry | platform | discovery and non-blocking telemetry | platform/optional | bounded failure |

The inventory deliberately contains one UNKNOWN external provider row. It is
disabled in the first deployment and owned by K6 plus the vendor-integration
track; it is not permission to guess a hostname or open egress.
