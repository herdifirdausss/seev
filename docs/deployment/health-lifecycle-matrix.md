# Health and lifecycle matrix

Health is liveness; readiness is dependency admission control. Kubernetes must
not use a public route or an unauthenticated debug route as a readiness proof.

| Service class | Liveness | Readiness inputs | Shutdown / recovery |
|---|---|---|---|
| Gateway / Auth | /health on public listener; internal health implementation | owned DB plus required service dependencies | stop admission, cancel workers, bounded HTTP shutdown |
| Ledger | /health and /ready | Postgres, Redis, RabbitMQ, currency/config load | stop workers and gRPC/HTTP gracefully; outbox remains durable |
| Payin / Payout / Fraud | /health, /ready where registered, internal metrics | Postgres plus required ledger/risk/vendor or broker dependencies | pending/idempotent recovery; no duplicate money action |
| Admin BFF | /health on private TLS listener | Postgres and auth/session dependencies | close admin sessions and scheduler |
| Assurance | /health, /ready | Postgres and read-only owner calls | pending correlation and alert |
| Vendor | /health, /ready | Postgres and callback/client wiring | retain callback state; direct egress is forbidden |
| PostgreSQL / Redis / RabbitMQ | native local diagnostics | broker/data process readiness | stateful recovery is K2/K7 |

Static startup and shutdown budgets are config-driven and are not claimed as
measured until runtime evidence is captured. Probe paths and ownership are in
[routes.yaml](../../deploy/inventory/routes.yaml); the generated local probe
output is under [service-probes](../evidence/k0/service-probes/).

Recovery classes:

- restart-safe: stateless request servers and idempotent consumers;
- durable-retry: outbox, callback, and provider workflows;
- singleton-scheduled: lock/lease required before more than one active worker;
- operations-recovery: database, backup, and broker restoration owned by K7.
