# Capability inventory

> Reviewed 2026-08-05 against the working tree. This is an evidence matrix,
> not a release-approval record.

`implemented` in a repository column means that the implementation and a
direct, repeatable verification path are present. It does not mean that the
path passed in this review. `partially_implemented` means that code or a
verification path exists, but the required surface is incomplete.
`not_started` means that no direct path exists in the repository, and
`not_applicable` means that the verification dimension does not apply to that
capability. Live acceptance and production readiness are separate gates.

The current local snapshot includes the default Go test suite, repository
gates, and Docker-backed capability/platform/DR journeys:

- `GOCACHE=/tmp/seev-go-cache go test ./...` — pass; integration-tagged tests
  are not included;
- `KEEP_WORK_DIR=1 make capability-e2e` — pass on Docker Desktop
  (`desktop-linux`); scheduled execution, fee maker-checker, disbursement
  approval, payout unknown-state recovery, reconciliation correction, dispute
  lifecycle, FX quote/conversion/reconciliation, and command-policy allow/deny
  paths all passed, including ledger-balance and account-consistency checks;
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
- `make docs-check` — pass; the repository-structure reorganization evidence
  plan is present and all 329 Markdown files, local links, anchors, guides,
  language, visual, and interactive assets validate;
- `make improvement-check` — pass; this validates the matrix contract, not
  the live environment;
- this local run does not claim chaos, cloud/vendor, or production acceptance;
  those remain separate evidence gates.

| Capability | Code | Schema | Unit | Integration | E2E | Chaos | Runtime accepted | Production ready | Owner/evidence |
|---|---|---|---|---|---|---|---|---|---|
| Ledger posting and idempotency | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger; `services/ledger/internal/ledger/idempotency_digest_integration_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/chaos-test.sh` (scenarios 1 and 21) |
| Unified command policy boundary | implemented | implemented | implemented | implemented | implemented | partially_implemented | evidence_required | evidence_required | Ledger/Auth; `services/ledger/internal/ledger/command`, `services/ledger/internal/ledger/command/executor_test.go`, `services/ledger/internal/ledger/architecture_boundary_test.go`, `services/ledger/migrations/000042_money_movement_execution_audit.up.sql`, `scripts/capability-e2e.sh`; direct E2E assertions cover allowed public API, scheduler, approved bulk-disbursement, and denied disabled-subject callers |
| KYC/tenant execution state | implemented | implemented | implemented | implemented | implemented | partially_implemented | evidence_required | evidence_required | Auth/Ledger; `services/ledger/internal/ledger/command/executor_test.go`, `services/ledger/internal/transport/grpc/kyc_tier_integration_test.go`, `services/auth/internal/auth/kyc_integration_test.go`, `services/gateway/internal/merchant/repository/repository_integration_test.go`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/chaos-test.sh` (tenant scenarios) |
| Scheduled and recurring transactions | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger; `docs/acceptance/scheduled-transactions.md`, `services/ledger/internal/ledger/schedule/schedule_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`; direct durable create/run/occurrence E2E is now present, but no fault-injection scenario |
| Fee pricing and approvals | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger/Ops; `services/ledger/migrations/000044_fee_rule_maker_checker.up.sql`, `services/ledger/internal/feepolicy`, `services/ledger/internal/feepolicy/quote_integration_test.go`, `scripts/business-e2e.sh`, `scripts/capability-e2e.sh`; direct maker-submit-distinct-checker approval E2E is now present, but no fee-specific chaos scenario |
| Pay-in and callback reconciliation | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Payin/Ledger; `services/payin/internal/payin/payin_integration_test.go`, `services/payin/internal/payin/merchant_integration_test.go`, `docs/operations/runbooks/reconciliation-mismatch-runbook.md`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/chaos-test.sh` (scenarios 6 and 23) |
| Payout unknown-state recovery | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Payout/Ledger; `services/payout/internal/payout/payout_integration_test.go`, `services/payout/internal/payout/failover_integration_test.go`, `docs/acceptance/payout-recovery.md`, `scripts/capability-e2e.sh`, `scripts/chaos-test.sh` (scenarios 5, 8, and 11); direct E2E proves timeout, vendor pinning, service restart, same-vendor recovery, and settlement |
| Disbursement maker-checker | implemented | implemented | implemented | implemented | implemented | partially_implemented | evidence_required | evidence_required | Ledger/Ops; `services/ledger/migrations/000045_disbursement_processing_requires_approval.up.sql`, `services/ledger/internal/ledger/disbursement_invariant_test.go`, `services/ledger/internal/ledger/disbursement/disbursement_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`, `scripts/chaos-test.sh` (scenario 14 covers pause/resume maker-checker) |
| Chargeback dispute lifecycle | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger/Compliance; `docs/acceptance/disputes.md`, `services/ledger/internal/ledger/dispute/dispute_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `services/gateway/internal/notification/inbox/notify_integration_test.go`, `scripts/capability-e2e.sh`; direct open/evidence/resolve/audit E2E is now present, but no dispute-specific chaos scenario |
| Reconciliation and append-only corrections | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger/Ops; `services/ledger/internal/ledger/recon/recon_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `services/ledger/internal/ledger/cryptox_recon_integration_test.go`, `docs/operations/runbooks/reconciliation-mismatch-runbook.md`, `scripts/business-e2e.sh`, `scripts/capability-e2e.sh`; direct mismatch-to-pending-correction E2E is now present |
| Multi-currency and FX | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger; `internal/platform/money/currency`, `services/ledger/internal/processors/fx_test.go`, `services/ledger/internal/ledger/execquote_integration_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`; direct USD enable/quote/conversion/reconciliation E2E is now present, but no FX-specific chaos scenario |
| Transactional outbox | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger/Platform; `services/ledger/internal/worker/outbox_relay_test.go`, `services/ledger/internal/ledger/outbox_backoff_contract_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `docs/operations/runbooks/outbox-backlog-runbook.md`, `scripts/business-e2e.sh`, `scripts/chaos-test.sh` (scenarios 2 and 23) |
| Auth and KYC | implemented | implemented | implemented | implemented | implemented | partially_implemented | evidence_required | evidence_required | Auth; `services/auth/internal/auth/kyc.go`, `services/auth/internal/worker/expiry_test.go`, `services/auth/internal/auth/kyc_integration_test.go`, `scripts/business-e2e.sh`, `scripts/privacy-e2e-host.sh`, `scripts/chaos-test.sh` (privacy scenarios 15–20) |
| Container/Kubernetes deployment | implemented | not_applicable | not_applicable | implemented | implemented | not_started | evidence_required | evidence_required | Platform; `Dockerfile`, `docker-compose.yml`, `scripts/smoke-container.sh`, `deploy/helm/seev`, `Makefile`, `deploy/kubernetes/kind-config-single-node.yaml`, `deploy/kubernetes/scripts/bootstrap-local.sh`, `deploy/kubernetes/scripts/smoke.sh`, `deploy/kubernetes/scripts/e2e.sh`, `.github/workflows/kubernetes-validate.yml`; smoke-container proves the Docker round-trip and the local kind smoke/business journey proves Gateway/Auth/callback/ledger routing; runtime acceptance remains external evidence |
| IaC and workload identity | implemented | not_applicable | not_applicable | implemented | implemented | not_started | evidence_required | evidence_required | Platform; `deploy/terraform`, `scripts/ci/check-iac.sh`, `scripts/ci/check-platform-integration.sh`, `scripts/platform-e2e.sh`, `deploy/helm/seev/templates/serviceaccounts.yaml`, `.github/workflows/kubernetes-validate.yml`; the E2E renders Terraform/IAM contracts through Helm into every workload, while cloud apply remains an external authorization gate |
| Supply-chain controls | implemented | not_applicable | not_applicable | implemented | implemented | not_applicable | evidence_required | evidence_required | Platform/Security; `scripts/ci/supply-chain-check.sh`, `scripts/ci/supply-chain-e2e.sh`, `scripts/ci/verify-supply-chain-evidence.sh`, `.github/workflows/ci.yml`, `.github/workflows/release-provenance.yml`, `docs/acceptance/supply-chain.md`; local bundle verification is implemented, while protected-registry and admission evidence remain external |
| DR and restore | implemented | not_applicable | implemented | implemented | implemented | partially_implemented | evidence_required | evidence_required | Platform; `Makefile`, `scripts/dr-drill.sh`, `scripts/dr-e2e.sh`, `.github/workflows/dr-drill.yml`, `operations/recovery/drverify`, `operations/recovery/drreseed`, `docs/operations/backup-restore-evidence.md`; dr-e2e runs latest and PITR restore, cross-database verification, reseed, security fencing, and post-restore application smoke; retained production-shaped evidence remains external |

## Status interpretation

The matrix intentionally keeps chaos and live acceptance separate from
repository integration/E2E completion:

- repository integration and E2E can be complete while chaos coverage is still
  `partially_implemented` or `not_started`;
- `evidence_required` means that source code, CI configuration, or a local
  mock cannot replace a live database/cloud/vendor/registry/production-shaped
  run;
- a release cannot be marked runtime-accepted or production-ready until the
  corresponding retained evidence and owner sign-off exist.
