# Runtime Contract

> [Documentation home](../README.md) · [Deployment](README.md)

**Machine-readable source of truth: [`deploy/inventory/`](../../deploy/inventory/).** This
document is a generated view. When the two disagree, the YAML files are authoritative. Run
`make deployment-inventory-check` to verify freshness.

Nine deployable application services. The optional `mock-push-provider` is a support process,
not a business service.

---

## Services

| Canonical name | Entrypoint | Image | Class |
|---|---|---|---|
| gateway | `services/gateway/cmd/gateway` | `seev/gateway-service` | EDGE_LATENCY |
| auth | `services/auth/cmd/auth` | `seev/auth-service` | EDGE_LATENCY |
| ledger | `services/ledger/cmd/ledger` | `seev/ledger-service` | MONEY_CRITICAL |
| payin | `services/payin/cmd/payin` | `seev/payin-service` | MONEY_CRITICAL |
| payout | `services/payout/cmd/payout` | `seev/payout-service` | MONEY_CRITICAL |
| fraud | `services/fraud/cmd/fraud` | `seev/fraud-service` | MONEY_CRITICAL |
| admin-bff | `services/adminbff/cmd/adminbff` | `seev/admin-bff-service` | ADMIN_LOW_TRAFFIC |
| assurance | `services/assurance/cmd/assurance` | `seev/assurance-service` | AUDIT_BACKGROUND |
| vendor | `services/vendor-service/cmd/vendor` | `seev/vendor-service` | VENDOR_BOUNDARY |

Source: [`deploy/inventory/services.yaml`](../../deploy/inventory/services.yaml)

---

## Ports and protocols

| Service | Listener | Port | Protocol | mTLS | Exposure |
|---|---|---:|---|---|---|
| gateway | public | 8080 | HTTP | edge termination | PUBLIC_EDGE |
| gateway | internal | 8081 | HTTPS | yes | INTERNAL_HTTP |
| auth | public | 8082 | HTTP | edge termination | PUBLIC_EDGE |
| auth | internal | 8083 | HTTPS | yes | INTERNAL_HTTP |
| ledger | user + internal | 8090, 8091 | HTTPS | yes | private/internal |
| ledger | gRPC | 9091 | gRPC | yes | INTERNAL_GRPC |
| payin | admin/internal | 8092 | HTTPS | yes | PRIVATE_ADMIN |
| payin | gRPC | 9092 | gRPC | yes | INTERNAL_GRPC |
| payout | admin/internal | 8093 | HTTPS | yes | PRIVATE_ADMIN |
| payout | gRPC | 9093 | gRPC | yes | INTERNAL_GRPC |
| fraud | admin/internal | 8094 | HTTPS | yes | PRIVATE_ADMIN |
| fraud | gRPC | 9094 | gRPC | yes | INTERNAL_GRPC |
| admin-bff | console | 8095 | HTTPS | yes | PRIVATE_ADMIN |
| assurance | internal | 8096 | HTTPS | yes | PRIVATE_ADMIN |
| assurance | gRPC | 9096 | gRPC | yes | INTERNAL_GRPC |
| vendor | callback/admin | 8098 | HTTPS | yes | CALLBACK_EDGE |
| vendor | gRPC | 9098 | gRPC | yes | INTERNAL_GRPC |

Source: [`deploy/inventory/ports.yaml`](../../deploy/inventory/ports.yaml)

---

## Public routes

| Path prefix | Owner | Edge policy |
|---|---|---|
| `/api/v1/auth/*` | Auth | HTTPS, request ID, body cap, rate limit |
| `/api/v1/*` | Gateway | HTTPS, request ID, body cap, rate limit |
| `/api/v1/b2b/*` | Gateway | HTTPS, API-key scope; feature flag off by default |
| `/webhooks/{vendor}` | VendorService | HTTPS, POST only, body cap, source CIDR, Traefik→VS mTLS |
| `/` admin routes | Admin BFF | local-only; cloud remains private |

Never route `/health`, `/ready`, `/metrics`, internal admin APIs, or gRPC through the public
gateway. Source: [`deploy/inventory/routes.yaml`](../../deploy/inventory/routes.yaml)

---

## Internal call graph

| Caller | Destination | Protocol / port | Purpose |
|---|---|---|---|
| Gateway | Auth | HTTPS `8082`/`8083` | auth, contact resolution |
| Gateway | Ledger | mTLS HTTP `8090`/gRPC `9091` | user/API and ledger ops |
| Gateway | Payin/Payout | mTLS gRPC `9092`/`9093` | money-in/out orchestration |
| Payin/Payout | VendorService | mTLS gRPC `9098` | vendor session/submission |
| VendorService | Payin/Payout | mTLS gRPC `9092`/`9093` | normalized callback delivery |
| Assurance | Ledger/Payin/Payout | mTLS gRPC | read-only correlation |
| Admin BFF | Auth/Ledger/Payin/Payout/Fraud/Gateway | mTLS HTTPS | typed operator proxy |
| All pods | PostgreSQL | TCP `5432` | service-owned persistence |
| Gateway/Ledger/Fraud | Redis | TCP `6379` | rate limits, locks, velocity |
| Gateway/Ledger/Fraud/workers | RabbitMQ | TCP `5672` | event transport |

Source: [`deploy/inventory/dependencies.yaml`](../../deploy/inventory/dependencies.yaml)

---

## mTLS identities

| Workload | Identity (SPIFFE) | Allowed inbound peers |
|---|---|---|
| gateway | `spiffe://seev.local/ns/seev/sa/gateway` | dev-operator, Prometheus, auth, internal callers |
| auth | `spiffe://seev.local/ns/seev/sa/auth` | dev-operator, Prometheus, admin-bff, internal callers |
| ledger | `spiffe://seev.local/ns/seev/sa/ledger` | gateway, auth, payin, payout, assurance, admin-bff, vendor |
| payin | `spiffe://seev.local/ns/seev/sa/payin` | gateway, auth, admin-bff, vendor, assurance |
| payout | `spiffe://seev.local/ns/seev/sa/payout` | gateway, auth, admin-bff, vendor, assurance |
| fraud | `spiffe://seev.local/ns/seev/sa/fraud` | auth, payin, payout, admin-bff, ledger |
| admin-bff | `spiffe://seev.local/ns/seev/sa/admin-bff` | dev-operator, private admin ingress |
| assurance | `spiffe://seev.local/ns/seev/sa/assurance` | dev-operator, Prometheus, private admin |
| vendor | `spiffe://seev.local/ns/seev/sa/vendor` | callback edge, dev-operator, Prometheus, payin/payout |

Source: `internal/platform/security/tls`, each service's `cmd/*/main.go`, gRPC allowlists

---

## Messaging

| Exchange / queue | Owner | Consumers | Delivery |
|---|---|---|---|
| `ledger.events.notifications` | Ledger/RabbitMQ | Gateway notification workers | at-least-once; durable; event inbox deduplication |
| `ledger.transaction.posted.v1` | Ledger outbox relay | Gateway, Fraud velocity | at-least-once; durable outbox |
| `ledger.dispute.lifecycle.v1` | Ledger outbox relay | Gateway notification workers | at-least-once; durable outbox |

All queues are durable. Dead-letter queues exist for notification and outbox paths. Source:
`services/*/internal/`, `docker-compose.yml`, RabbitMQ management config.

---

## Background jobs

| Owner | Workers | Trigger | Replica rule |
|---|---|---|---|
| ledger | outbox relay, verifier, snapshot, schedule runner, accrual, retention | poll/cron | claims/lock; one active scheduler |
| auth | KYC retry, expiry, rescreen, retention, object outbox | ticker/lock/scheduler | database claims |
| payin | retention | scheduler | distributed lock |
| payout | resume, vendor relay, retention | cron/poll/scheduler | lock/claim |
| fraud | velocity consumer, spill flusher, retention | RabbitMQ/ticker/scheduler | competing consumers |
| gateway | notify, merchant relay/consumer, retention, observability | RabbitMQ/poll/scheduler | claims/consumer |
| admin-bff | session cleanup, retention | shared scheduler | memory lock |
| assurance | correlation, retention | module scheduler | one active policy |
| vendor | retention | scheduler | distributed lock |

Source: [`deploy/inventory/jobs.yaml`](../../deploy/inventory/jobs.yaml)

---

## Storage and data ownership

| Store | Owner | Data type | Persistence |
|---|---|---|---|
| `seev_gateway` PostgreSQL | Gateway | notifications, merchant API, webhooks | stateful; requires backup |
| `seev_auth` PostgreSQL | Auth | identity, sessions, KYC, privacy | stateful; requires backup |
| `seev_ledger` PostgreSQL | Ledger | accounts, entries, reconciliation, schedules | stateful; requires backup |
| `seev_payin` PostgreSQL | Payin | intents, routing, callbacks | stateful; requires backup |
| `seev_payout` PostgreSQL | Payout | requests, vendor calls, routing | stateful; requires backup |
| `seev_fraud` PostgreSQL | Fraud | screening events, rule modes, sanctions | stateful; requires backup |
| `seev_adminbff` PostgreSQL | Admin BFF | sessions, audit log | stateful |
| `seev_assurance` PostgreSQL | Assurance | runs, findings, cursors | stateful |
| `seev_vendor` PostgreSQL | VendorService | callbacks inbox, outbound attempts | stateful; requires backup |
| Redis | shared runtime | rate limits, leases, velocity, cache | ephemeral; non-authoritative |
| RabbitMQ | messaging | durable queues, DLQs | broker-managed |
| Object store | Auth | KYC documents, exports | external; mock local |

Source: [`deploy/inventory/data-stores.yaml`](../../deploy/inventory/data-stores.yaml)

---

## Secrets

| Category | Consumers | Sharing scope | Rotation policy |
|---|---|---|---|
| Database + broker credentials | data/messaging clients | per-service in cloud | coordinated rotation |
| JWT + internal service token | auth, internal policy | contract-shared | versioned/coordinated |
| cryptox and key rings | ledger, payout, gateway, vendor | controlled consumer set | versioned key ring |
| Merchant pepper + vendor HMAC | gateway, vendor | owner-scoped | rehash/provider coordinated |
| KYC provider token | auth | auth-only | provider rotation |
| TLS CA + leaf private keys | certgen, per service | CA or per-workload | secret-manager lifecycle |
| Backup repository credentials | backup-agent | operations-only | repository lifecycle |
| Alert webhook credential | assurance | assurance-only | provider lifecycle |

Source: [`deploy/inventory/secrets.yaml`](../../deploy/inventory/secrets.yaml)

---

## Health and lifecycle

| Service class | Liveness | Readiness inputs | Shutdown |
|---|---|---|---|
| Gateway / Auth | `/health` on public listener | owned DB + required dependencies | stop admission, cancel workers, bounded HTTP shutdown |
| Ledger | `/health`, `/ready` | Postgres, Redis, RabbitMQ, config load | stop workers and gRPC/HTTP gracefully |
| Payin / Payout / Fraud | `/health`, `/ready` | Postgres + required dependencies | pending/idempotent recovery; no duplicate money action |
| Admin BFF | `/health` on private TLS | Postgres + session dependencies | close admin sessions and scheduler |
| Assurance | `/health`, `/ready` | Postgres + read-only owner calls | pending correlation |
| Vendor | `/health`, `/ready` | Postgres + callback/client wiring | retain callback state; direct egress forbidden |

Source: [`deploy/inventory/routes.yaml`](../../deploy/inventory/routes.yaml)

---

## Resource baseline

Resource classes: EDGE_LATENCY (gateway/auth), MONEY_CRITICAL (ledger/payin/payout/fraud),
ADMIN_LOW_TRAFFIC (admin-bff), AUDIT_BACKGROUND (assurance), VENDOR_BOUNDARY (vendor).

No final CPU/memory request or limit is accepted from local Compose samples alone. Kubernetes
sizing requires a production-shaped load run. See [`deploy/inventory/resource-baseline.yaml`](../../deploy/inventory/resource-baseline.yaml)
and [`docs/performance/capacity-model.md`](../performance/capacity-model.md).

---

## Vendor network access

All outbound vendor traffic is routed exclusively through Squid (`vendor:3128`). Direct
egress from application pods is denied by Kubernetes NetworkPolicy. The destination ACL limits
access to approved vendor endpoint domains. Source: `deploy/kubernetes/config/squid.conf`,
[`deploy/inventory/vendor-network.yaml`](../../deploy/inventory/vendor-network.yaml).
