# mTLS identity matrix

The shared CA signs one leaf per service identity. A Kubernetes service name
is not automatically the same thing as the application or certificate
identity; the mapping below is explicit.

| Workload | Identity | Allowed inbound peers | Typical outbound peers |
|---|---|---|---|
| gateway | spiffe://seev.local/ns/seev/sa/gateway | dev-operator, Prometheus, auth and configured internal callers | ledger, payin, payout |
| auth | spiffe://seev.local/ns/seev/sa/auth | dev-operator, Prometheus, admin-bff, configured internal callers | ledger, payin, payout, fraud, gateway |
| ledger | spiffe://seev.local/ns/seev/sa/ledger | gateway, auth, payin, payout, assurance, admin-bff, vendor | RabbitMQ, Postgres, Redis |
| payin | spiffe://seev.local/ns/seev/sa/payin | gateway, auth, admin-bff, vendor, assurance | ledger, fraud, vendor |
| payout | spiffe://seev.local/ns/seev/sa/payout | gateway, auth, admin-bff, vendor, assurance | ledger, fraud, vendor |
| fraud | spiffe://seev.local/ns/seev/sa/fraud | auth, payin, payout, admin-bff, ledger | RabbitMQ, Postgres, Redis |
| admin-bff | spiffe://seev.local/ns/seev/sa/admin-bff | dev-operator, private admin ingress | auth, ledger, payin, payout, fraud, gateway |
| assurance | spiffe://seev.local/ns/seev/sa/assurance | dev-operator, Prometheus, private admin | ledger, payin, payout |
| vendor | spiffe://seev.local/ns/seev/sa/vendor | callback edge, dev-operator, Prometheus, payin/payout callbacks | payin, payout, Squid when real provider is enabled |

Public edge exceptions are Gateway 8080 and Auth 8082. Callback ingress is
signature-checked and then mTLS-protected from edge to VendorService. Health
probes must use an explicitly allowed probe identity or the documented local
probe exception; metrics remain internal.

Sources: pkg/tlsx, each service cmd/*/main.go, the gRPC server allowlists,
and [dependencies.yaml](../../deploy/inventory/dependencies.yaml).
