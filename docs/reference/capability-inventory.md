# Capability inventory

> Reviewed 2026-08-05 against the working tree. This is an evidence matrix,
> not a release-approval record.

`implemented` in a repository column means that the implementation and a
direct, repeatable verification path are present. It does not mean that the
path passed in this review. `partially_implemented` means that code or a
verification path exists, but the required surface is incomplete.
`not_started` means that no direct path exists in the repository, and
`not_applicable` means that the verification dimension does not apply to that
capability. `accepted` in the Runtime accepted column means the disposable
local runtime profile produced a valid manifest for the reviewed commit;
cloud/vendor/production evidence and owner sign-off remain separate
production-readiness gates.

The current local snapshot includes the default Go test suite, repository
gates, and Docker-backed capability/platform/DR journeys:

- `GOCACHE=/tmp/seev-go-cache go test ./...` — pass; integration-tagged tests
  are not included;
- `KEEP_WORK_DIR=1 make capability-e2e` — pass on Docker Desktop
  (`desktop-linux`); scheduled execution, fee maker-checker, disbursement
  approval, payout unknown-state recovery, reconciliation correction, dispute
  lifecycle, FX quote/conversion/reconciliation, and command-policy allow/deny
  paths all passed, including ledger-balance and account-consistency checks;
- `KEEP_WORK_DIR=1 make capability-chaos` — pass on Docker Desktop;
  auth/KYC and tenant-state recovery plus scheduled, fee, disbursement,
  reconciliation, dispute, FX, payout, and command-policy paths each survived
  a real service kill, failed closed during the outage, and converged after
  restart;
- `make platform-chaos` — pass; a deliberately removed AWS workload-identity
  annotation was rejected at the rendered ServiceAccount boundary, while the
  local overlay remained cloud-identity free;
- `./scripts/ci/check-platform-integration.sh` — pass; all four Terraform
  modules validate backend-free, all Helm overlays lint/render, and AWS/GCP
  workload-identity annotations reach the Vendor ServiceAccount;
- `make platform-e2e` — pass; rendered Terraform/IAM and Helm contracts reach
  every application workload and the public Gateway;
- `make supply-chain-e2e` — pass; source controls and the protected-release
  evidence verifier pass a complete digest/SBOM/provenance/signature bundle;
- `AUTH_APP_PORT=28082 AUTH_INTERNAL_PORT=28083 make dr-e2e` — pass on Docker
  Desktop; latest-backup and PITR restore, cross-database verification, Redis
  reseed, security fencing, post-restore application smoke, and Assurance
  backfill passed (RPO 0s; latest RTO 61s, PITR RTO 89s);
- `KIND_CONFIG=deploy/kubernetes/kind-config-single-node.yaml make k8s-smoke`
  and `deploy/kubernetes/scripts/e2e.sh` — pass on local kind; Calico
  NetworkPolicy preflight, Gateway readiness, Auth routing, register/login/KYC,
  signed callback settlement, and cross-user ledger transfer all passed;
- `make k8s-chaos` — pass on a disposable kind cluster; one Gateway pod was
  deleted, replaced with a new UID, and the recovered edge smoke passed;
- `make docs-check` — pass; the repository-structure reorganization evidence
  plan is present and all 333 Markdown files, local links, anchors, guides,
  language, visual, and interactive assets validate;
- `make improvement-check` — pass; this validates the matrix contract, not
  the live environment;
- `RUNTIME_ACCEPTANCE_DIR=/tmp/seev-runtime-acceptance-evidence ./scripts/runtime-acceptance.sh`
  — the complete local runtime-acceptance implementation; it records one
  sanitized manifest and one accepted result for each of the 17 capabilities
  after the application, container, platform, Kubernetes, supply-chain, and
  DR stages pass;
- the local snapshot proves repository/application, rendered-IaC, and
  disposable-kind chaos harnesses. Kubernetes pod replacement and the isolated
  DR destruction/restore drill are included in the explicit runtime profile;
  none of these results claims cloud/vendor or production readiness.

The runtime evidence contract and capability-to-stage mapping are recorded in
[`docs/operations/runtime-acceptance.md`](../operations/runtime-acceptance.md),
implemented by [`scripts/runtime-acceptance.sh`](../../scripts/runtime-acceptance.sh),
and independently checked by
[`scripts/ci/check-runtime-acceptance.sh`](../../scripts/ci/check-runtime-acceptance.sh).

| Capability | Code | Schema | Unit | Integration | E2E | Chaos | Runtime accepted | Production ready | Owner/evidence |
|---|---|---|---|---|---|---|---|---|---|
| Ledger posting and idempotency | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger; `services/ledger/internal/ledger/idempotency_digest_integration_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/chaos-test.sh` (scenarios 1 and 21) |
| Unified command policy boundary | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger/Auth; `services/ledger/internal/ledger/command`, `services/ledger/internal/ledger/command/executor_test.go`, `services/ledger/internal/ledger/architecture_boundary_test.go`, `services/ledger/migrations/000042_money_movement_execution_audit.up.sql`, `scripts/capability-e2e.sh`, `scripts/capability-chaos.sh`; chaos probes ledger unavailability and verifies denied disabled-subject audit decisions |
| KYC/tenant execution state | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Auth/Ledger; `services/ledger/internal/ledger/command/executor_test.go`, `services/ledger/internal/transport/grpc/kyc_tier_integration_test.go`, `services/auth/internal/auth/kyc_integration_test.go`, `services/gateway/internal/merchant/repository/repository_integration_test.go`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/capability-e2e.sh`; chaos kills Auth, verifies KYC recovery, and rejects a disabled tenant projection |
| Scheduled and recurring transactions | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger; `docs/operations/runbooks/stuck-state-runbook.md`, `services/ledger/internal/ledger/schedule/schedule_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`, `scripts/capability-chaos.sh`; schedule state survives a killed Ledger dispatcher and executes one durable occurrence after restart |
| Fee pricing and approvals | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger/Ops; `services/ledger/migrations/000044_fee_rule_maker_checker.up.sql`, `services/ledger/internal/feepolicy`, `services/ledger/internal/feepolicy/quote_integration_test.go`, `scripts/business-e2e.sh`, `scripts/capability-e2e.sh`, `scripts/capability-chaos.sh`; submitted fee approval fails closed during Ledger outage and succeeds with a distinct checker after restart |
| Pay-in and callback reconciliation | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Payin/Ledger; `services/payin/internal/payin/payin_integration_test.go`, `services/payin/internal/payin/merchant_integration_test.go`, `docs/operations/runbooks/reconciliation-mismatch-runbook.md`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/chaos-test.sh` (scenarios 6 and 23) |
| Payout unknown-state recovery | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Payout/Ledger; `services/payout/internal/payout/payout_integration_test.go`, `services/payout/internal/payout/failover_integration_test.go`, `docs/operations/runbooks/payout-unknown-state-runbook.md`, `scripts/capability-e2e.sh`, `scripts/chaos-test.sh` (scenarios 5, 8, and 11); direct E2E proves timeout, vendor pinning, service restart, same-vendor recovery, and settlement |
| Disbursement maker-checker | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger/Ops; `services/ledger/migrations/000045_disbursement_processing_requires_approval.up.sql`, `services/ledger/internal/ledger/disbursement_invariant_test.go`, `services/ledger/internal/ledger/disbursement/disbursement_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`, `scripts/capability-chaos.sh`; imported batches remain unprocessable during Ledger outage and still require a distinct checker after restart |
| Chargeback dispute lifecycle | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger/Compliance; `docs/operations/runbooks/stuck-state-runbook.md`, `services/ledger/internal/ledger/dispute/dispute_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `services/gateway/internal/notification/inbox/notify_integration_test.go`, `scripts/capability-e2e.sh`, `scripts/capability-chaos.sh`; open dispute/evidence state survives Ledger outage and resolves with an auditable terminal transition |
| Reconciliation and append-only corrections | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger/Ops; `services/ledger/internal/ledger/recon/recon_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `services/ledger/internal/ledger/cryptox_recon_integration_test.go`, `docs/operations/runbooks/reconciliation-mismatch-runbook.md`, `scripts/business-e2e.sh`, `scripts/capability-e2e.sh`, `scripts/capability-chaos.sh`; mismatch evidence remains readable and correction creation resumes after Ledger outage |
| Multi-currency and FX | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger; `internal/platform/money/currency`, `services/ledger/internal/processors/fx_test.go`, `services/ledger/internal/ledger/execquote_integration_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`, `scripts/capability-chaos.sh`; quoted conversion fails closed while Ledger is down and posts once after recovery |
| Transactional outbox | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger/Platform; `services/ledger/internal/worker/outbox_relay_test.go`, `services/ledger/internal/ledger/outbox_backoff_contract_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `docs/operations/runbooks/outbox-backlog-runbook.md`, `scripts/business-e2e.sh`, `scripts/chaos-test.sh` (scenarios 2 and 23) |
| Auth and KYC | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Auth; `services/auth/internal/auth/kyc.go`, `services/auth/internal/worker/expiry_test.go`, `services/auth/internal/auth/kyc_integration_test.go`, `scripts/business-e2e.sh`, `scripts/privacy-e2e-host.sh`, `scripts/chaos-test.sh` (privacy scenarios 15–20), `scripts/capability-e2e.sh`; real Auth kill/login failure/KYC refresh recovery is exercised in capability chaos |
| Container/Kubernetes deployment | implemented | not_applicable | not_applicable | implemented | implemented | implemented | evidence_required | evidence_required | Platform; `Dockerfile`, `docker-compose.yml`, `scripts/smoke-container.sh`, `deploy/helm/seev`, `Makefile`, `deploy/kubernetes/kind-config-single-node.yaml`, `deploy/kubernetes/scripts/bootstrap-local.sh`, `deploy/kubernetes/scripts/smoke.sh`, `deploy/kubernetes/scripts/e2e.sh`, `deploy/kubernetes/scripts/chaos.sh`, `.github/workflows/kubernetes-validate.yml`; make k8s-chaos deletes one Gateway pod, verifies replacement/readiness, and reruns edge smoke |
| IaC and workload identity | implemented | not_applicable | not_applicable | implemented | implemented | implemented | evidence_required | evidence_required | Platform; `deploy/terraform`, `scripts/ci/check-iac.sh`, `scripts/ci/check-platform-integration.sh`, `scripts/platform-e2e.sh`, `scripts/ci/check-rendered-workload-identity.sh`, `scripts/ci/platform-chaos.sh`, `deploy/helm/seev/templates/serviceaccounts.yaml`, `.github/workflows/kubernetes-validate.yml`; the chaos drill removes the rendered provider annotation and requires the workload-boundary verifier to fail |
| Supply-chain controls | implemented | not_applicable | not_applicable | implemented | implemented | not_applicable | evidence_required | evidence_required | Platform/Security; `scripts/ci/supply-chain-check.sh`, `scripts/ci/supply-chain-e2e.sh`, `scripts/ci/verify-supply-chain-evidence.sh`, `.github/workflows/ci.yml`, `.github/workflows/release-provenance.yml`, `docs/security/supply-chain.md`; local bundle verification is implemented, while protected-registry and admission evidence remain external |
| DR and restore | implemented | not_applicable | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Platform; `Makefile`, `scripts/dr-drill.sh`, `scripts/dr-e2e.sh`, `.github/workflows/dr-drill.yml`, `operations/recovery/drverify`, `operations/recovery/drreseed`, `docs/operations/backup-restore-evidence.md`; the isolated latest/PITR drill destroys only its gameday volumes, restores, reseeds, fences sessions, and proves post-restore application recovery |

## Status interpretation

The matrix keeps repository implementation, local runtime acceptance, and
production readiness separate:

- `Chaos=implemented` means the repository has a direct fault-injection and
  recovery path for the capability; it does not mean that every cloud,
  vendor, Kubernetes, or production-shaped run has passed;
- `Runtime accepted=accepted` requires the passing manifest validated by
  `scripts/ci/check-runtime-acceptance.sh`; a source path or a local mock
  alone is insufficient;
- `evidence_required` remains the status until that manifest exists for the
  reviewed commit, and production readiness additionally requires the
  corresponding retained cloud/vendor/registry/production-shaped evidence
  and owner sign-off.
