# Service and port matrix

All application listeners use the repository's mTLS conventions unless the
listener is explicitly described as a public edge. A listening port is not
itself a Kubernetes exposure decision.

| Workload | HTTP listener(s) | gRPC | Kubernetes exposure | Health / metrics | Ownership |
|---|---:|---:|---|---|---|
| Gateway | `8080` public, `8081` internal | — | `ClusterIP`; public route only to `8080` | `/health`, `/ready`, `/metrics` on internal listener | Gateway |
| Auth | `8082` public auth, `8083` internal/admin | — | `ClusterIP`; public auth route only after edge review | `/health`; metrics on internal listener | Auth |
| Ledger | `8090` user, `8091` internal | `9091` | `ClusterIP`; no direct public Service | `/health`, `/ready`, `/metrics` | Ledger |
| Payin | `8092` internal/admin | `9092` | `ClusterIP`; no direct public Service | `/health`, `/ready`, `/metrics` | Payin |
| Payout | `8093` internal/admin | `9093` | `ClusterIP`; no direct public Service | `/health`, `/ready`, `/metrics` | Payout |
| Fraud | `8094` internal/admin | `9094` | `ClusterIP`; no direct public Service | `/health`, `/ready`, `/metrics` | Fraud |
| Admin BFF | `8095` internal admin | — | `ClusterIP`; port-forward/tunnel only in first cloud stage | `/health`, `/ready`, `/metrics` | Admin BFF |
| Assurance | `8096` internal/admin | — | `ClusterIP`; no direct public Service | `/health`, `/ready`, `/metrics` | Assurance |
| VendorService | `8098` callback/admin | `9098` | `ClusterIP`; callback route only | `/health`, `/ready`, `/metrics` | VendorService |
| PostgreSQL | `5432` | — | headless/ClusterIP, namespace-local | `pg_isready` | Data |
| Redis | `6379` | — | headless/ClusterIP, namespace-local | `redis-cli ping` | Data |
| RabbitMQ | `5672`; management `15672` | — | AMQP ClusterIP; management disabled externally | broker diagnostics | Data |
| Squid | proxy `3128` | — | ClusterIP in `seev-egress` only | TCP / proxy metrics | Egress |

Source references: `docker-compose.yml`, each `cmd/*/main.go`, and the
listener registrations in `internal/*`.
