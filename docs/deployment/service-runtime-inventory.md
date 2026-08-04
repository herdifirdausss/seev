# Service runtime inventory

K0 canonicalizes the nine core deployable application processes. The optional
local mock push provider is a support process and is not part of the business
service count. Kubernetes names
are intentionally shorter than the current application names; the mTLS
identity mapping is separate and is recorded in
[mtls-identity-matrix.md](mtls-identity-matrix.md).

| Canonical name | Entrypoint | Compose service / build value | Image | HTTP / gRPC | Workers | First deployment |
|---|---|---|---|---|---|---|
| gateway | services/gateway/cmd/gateway | gateway-service / gateway | seev/gateway-service | 8080, 8081 / — | merchant relay/consumer, notifications, retention, metrics refresh | enabled |
| auth | services/auth/cmd/auth | auth-service / auth-service | seev/auth-service | 8082, 8083 / — | KYC retry/expiry/rescreen, retention, object outbox | enabled |
| ledger | services/ledger/cmd/ledger | ledger-service / ledger-service | seev/ledger-service | 8090, 8091 / 9091 | outbox, verifier, snapshot, schedules, accrual, retention | enabled |
| payin | services/payin/cmd/payin | payin-service / payin-service | seev/payin-service | 8092 / 9092 | retention | enabled |
| payout | services/payout/cmd/payout | payout-service / payout-service | seev/payout-service | 8093 / 9093 | recovery, vendor relay, retention | enabled |
| fraud | services/fraud/cmd/fraud | fraud-service / fraud-service | seev/fraud-service | 8094 / 9094 | velocity consumer, spill flusher, retention | enabled |
| admin-bff | services/adminbff/cmd/adminbff | admin-bff-service / admin-bff-service | seev/admin-bff-service | 8095 / — | session cleanup, retention | enabled |
| assurance | services/assurance/cmd/assurance | assurance-service / assurance-service | seev/assurance-service | 8096 / 9096 | correlation, retention | enabled |
| vendor | services/vendor-service/cmd/vendor | vendor-service / vendor-service | seev/vendor-service | 8098 / 9098 | retention | enabled |

The request server and workers currently share one process for every
application service. No dedicated worker binary exists. Replicas therefore
must begin conservatively for memory-lock schedulers and be increased only
after K3 applies the lock/lease contract.

One-shot and support tools are classified in
[services.yaml](../../deploy/inventory/services.yaml): tools/migrate becomes a
migration Job, tools/certgen is a provisioning tool, operations/agents/backup/cmd/backup-agent is
operations-only, and the remaining administrative, load, fixture, contract,
and documentation commands remain local or CI utilities. Compose's
object-store-init is a local one-shot initializer, not the migration runner.

Sources: services/*/cmd/*/main.go, tools/*, operations/*, Dockerfile, docker-compose.yml,
[services.yaml](../../deploy/inventory/services.yaml), and the generated
[Compose service list](../evidence/k0/command-output/compose-app-services.txt).
