# Service runtime inventory

K0 canonicalizes the nine core deployable application processes. The optional
local mock push provider is a support process and is not part of the business
service count. Kubernetes names
are intentionally shorter than the current application names; the mTLS
identity mapping is separate and is recorded in
[mtls-identity-matrix.md](mtls-identity-matrix.md).

| Canonical name | Entrypoint | Compose service / build value | Image | HTTP / gRPC | Workers | First deployment |
|---|---|---|---|---|---|---|
| gateway | cmd/gateway | gateway-service / gateway | seev/gateway-service | 8080, 8081 / — | merchant relay/consumer, notifications, retention, metrics refresh | enabled |
| auth | cmd/auth-service | auth-service / auth-service | seev/auth-service | 8082, 8083 / — | KYC retry/expiry/rescreen, retention, object outbox | enabled |
| ledger | cmd/ledger-service | ledger-service / ledger-service | seev/ledger-service | 8090, 8091 / 9091 | outbox, verifier, snapshot, schedules, accrual, retention | enabled |
| payin | cmd/payin-service | payin-service / payin-service | seev/payin-service | 8092 / 9092 | retention | enabled |
| payout | cmd/payout-service | payout-service / payout-service | seev/payout-service | 8093 / 9093 | recovery, vendor relay, retention | enabled |
| fraud | cmd/fraud-service | fraud-service / fraud-service | seev/fraud-service | 8094 / 9094 | velocity consumer, spill flusher, retention | enabled |
| admin-bff | cmd/admin-bff-service | admin-bff-service / admin-bff-service | seev/admin-bff-service | 8095 / — | session cleanup, retention | enabled |
| assurance | cmd/assurance-service | assurance-service / assurance-service | seev/assurance-service | 8096 / 9096 | correlation, retention | enabled |
| vendor | cmd/vendor-service | vendor-service / vendor-service | seev/vendor-service | 8098 / 9098 | retention | enabled |

The request server and workers currently share one process for every
application service. No dedicated worker binary exists. Replicas therefore
must begin conservatively for memory-lock schedulers and be increased only
after K3 applies the lock/lease contract.

One-shot and support tools are classified in
[services.yaml](../../deploy/inventory/services.yaml): cmd/migrate becomes a
migration Job, cmd/certgen is a provisioning tool, cmd/backup-agent is
operations-only, and the remaining administrative, load, fixture, contract,
and documentation commands remain local or CI utilities. Compose's
object-store-init is a local one-shot initializer, not the migration runner.

Sources: cmd/*/main.go, Dockerfile, docker-compose.yml,
[services.yaml](../../deploy/inventory/services.yaml), and the generated
[Compose service list](../evidence/k0/command-output/compose-app-services.txt).
