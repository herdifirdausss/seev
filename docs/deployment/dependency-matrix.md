# Runtime dependency matrix

`allow` means the dependency is required by the first sandbox deployment.
Anything not listed is denied by the namespace NetworkPolicies.

| Caller | Destination | Protocol / port | Purpose | Failure posture |
|---|---|---:|---|---|
| All application pods | PostgreSQL | TCP `5432` | service-owned persistence | readiness fails; no cross-database grants |
| Gateway, Ledger, Payin, Payout, Fraud | Redis | TCP `6379` | rate limits, locks, velocity, cache | feature-specific fail-closed behavior |
| Gateway, Ledger, Fraud, and workers | RabbitMQ | TCP `5672` | event transport | reconnect and durable outbox/replay |
| Gateway | Auth | HTTPS `8082`/`8083` | auth and privacy composition | request failure, no direct data access |
| Gateway | Ledger | mTLS HTTP `8090`/`8091`, gRPC `9091` | user/API and ledger operations | request failure |
| Gateway | Payin/Payout | mTLS gRPC `9092`/`9093` | money-in/out orchestration | request failure |
| Payin/Payout | VendorService | mTLS gRPC `9098` | vendor boundary | retry/recovery; no raw callback ownership |
| VendorService | Payin/Payout | mTLS gRPC `9092`/`9093` | normalized callback delivery | callback is retained/retried |
| Assurance | Ledger/Payin/Payout | mTLS gRPC | independent checks | findings, never balance edits |
| Admin BFF | Auth/Ledger/Payin/Payout/Fraud/Gateway | mTLS HTTPS | typed operator proxy | operator request fails safely |
| All application pods | DNS | UDP/TCP `53` | service/vendor resolution | readiness or outbound operation fails |
| VendorService | Squid | TCP `3128` | explicit vendor egress only | direct path denied; proxy error is visible |
| Squid | approved vendor endpoints | TCP `443` | outbound vendor API | destination ACL and NAT apply |
| All pods | telemetry collector/Prometheus | configured internal ports | metrics/traces | business traffic continues if telemetry fails |

The first cloud stage keeps Admin BFF private and does not expose RabbitMQ
management, PostgreSQL, Redis, metrics, or the Traefik dashboard.
