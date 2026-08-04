# Capability inventory

> Reviewed 2026-08-04 against the working tree. This is an evidence matrix,
> not a release-approval record.

`implemented` in a repository column means that the implementation and a
direct, repeatable verification path are present. It does not mean that the
path passed in this review. `partially_implemented` means that code or a
verification path exists, but the required surface is incomplete.
`not_started` means that no direct path exists in the repository, and
`not_applicable` means that the verification dimension does not apply to that
capability. Live acceptance and production readiness are separate gates.

The current local snapshot includes the default Go test suite, static
documentation/gate checks, and one Docker-backed capability journey:

- `GOCACHE=/tmp/seev-go-cache go test ./...` — pass; integration-tagged tests
  are not included;
- `KEEP_WORK_DIR=1 make capability-e2e` — pass on Docker Desktop
  (`desktop-linux`); scheduled execution, fee maker-checker, disbursement
  approval, reconciliation correction, dispute lifecycle, and FX
  quote/conversion/reconciliation all passed, including ledger-balance and
  account-consistency checks;
- `./scripts/ci/check-platform-integration.sh` — pass; all four Terraform
  modules validate backend-free, all Helm overlays lint/render, and AWS/GCP
  workload-identity annotations reach the Vendor ServiceAccount;
- `KEEP_WORK_DIR=1 ./scripts/dr-drill.sh latest` — pass on Docker Desktop;
  latest-backup restore, cross-database verification, Redis reseed, security
  fencing, post-restore application smoke, and Assurance backfill passed
  (RPO 0s, RTO 39s);
- `make docs-check` — pass;
- `make improvement-check` — pass; this validates the matrix contract, not
  the live environment;
- this local run does not claim chaos, Kubernetes runtime, cloud/vendor, or
  production acceptance; those remain separate evidence gates.

| Capability | Code | Schema | Unit | Integration | E2E | Chaos | Runtime accepted | Production ready | Owner/evidence |
|---|---|---|---|---|---|---|---|---|---|
| Ledger posting and idempotency | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger; `services/ledger/internal/ledger/idempotency_digest_integration_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/chaos-test.sh` (scenarios 1 and 21) |
| Unified command policy boundary | implemented | implemented | implemented | implemented | partially_implemented | partially_implemented | evidence_required | evidence_required | Ledger/Auth; `services/ledger/internal/ledger/command`, `services/ledger/internal/ledger/command/executor_test.go`, `services/ledger/internal/ledger/architecture_boundary_test.go`, `services/ledger/migrations/000042_money_movement_execution_audit.up.sql`, `scripts/capability-e2e.sh`; direct audit assertions cover public API, scheduler, and approved bulk-disbursement callers |
| KYC/tenant execution state | implemented | implemented | implemented | implemented | implemented | partially_implemented | evidence_required | evidence_required | Auth/Ledger; `services/ledger/internal/ledger/command/executor_test.go`, `services/ledger/internal/transport/grpc/kyc_tier_integration_test.go`, `services/auth/internal/auth/kyc_integration_test.go`, `services/gateway/internal/merchant/repository/repository_integration_test.go`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/chaos-test.sh` (tenant scenarios) |
| Scheduled and recurring transactions | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger; `docs/acceptance/scheduled-transactions.md`, `services/ledger/internal/ledger/schedule/schedule_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`; direct durable create/run/occurrence E2E is now present, but no fault-injection scenario |
| Fee pricing and approvals | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger/Ops; `services/ledger/migrations/000044_fee_rule_maker_checker.up.sql`, `services/ledger/internal/feepolicy`, `services/ledger/internal/feepolicy/quote_integration_test.go`, `scripts/business-e2e.sh`, `scripts/capability-e2e.sh`; direct maker-submit-distinct-checker approval E2E is now present, but no fee-specific chaos scenario |
| Pay-in and callback reconciliation | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Payin/Ledger; `services/payin/internal/payin/payin_integration_test.go`, `services/payin/internal/payin/merchant_integration_test.go`, `docs/operations/runbooks/reconciliation-mismatch-runbook.md`, `scripts/business-e2e.sh`, `scripts/merchant-e2e.sh`, `scripts/chaos-test.sh` (scenarios 6 and 23) |
| Payout unknown-state recovery | implemented | implemented | implemented | implemented | partially_implemented | implemented | evidence_required | evidence_required | Payout/Ledger; `services/payout/internal/payout/payout_integration_test.go`, `services/payout/internal/payout/failover_integration_test.go`, `docs/acceptance/payout-recovery.md`, `scripts/chaos-test.sh` (scenarios 5, 8, and 11); no dedicated full business E2E for unknown-state recovery |
| Disbursement maker-checker | implemented | implemented | implemented | implemented | implemented | partially_implemented | evidence_required | evidence_required | Ledger/Ops; `services/ledger/migrations/000045_disbursement_processing_requires_approval.up.sql`, `services/ledger/internal/ledger/disbursement_invariant_test.go`, `services/ledger/internal/ledger/disbursement/disbursement_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`, `scripts/chaos-test.sh` (scenario 14 covers pause/resume maker-checker) |
| Chargeback dispute lifecycle | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger/Compliance; `docs/acceptance/disputes.md`, `services/ledger/internal/ledger/dispute/dispute_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `services/gateway/internal/notification/inbox/notify_integration_test.go`, `scripts/capability-e2e.sh`; direct open/evidence/resolve/audit E2E is now present, but no dispute-specific chaos scenario |
| Reconciliation and append-only corrections | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger/Ops; `services/ledger/internal/ledger/recon/recon_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `services/ledger/internal/ledger/cryptox_recon_integration_test.go`, `docs/operations/runbooks/reconciliation-mismatch-runbook.md`, `scripts/business-e2e.sh`, `scripts/capability-e2e.sh`; direct mismatch-to-pending-correction E2E is now present |
| Multi-currency and FX | implemented | implemented | implemented | implemented | implemented | not_started | evidence_required | evidence_required | Ledger; `internal/platform/money/currency`, `services/ledger/internal/processors/fx_test.go`, `services/ledger/internal/ledger/execquote_integration_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `scripts/capability-e2e.sh`; direct USD enable/quote/conversion/reconciliation E2E is now present, but no FX-specific chaos scenario |
| Transactional outbox | implemented | implemented | implemented | implemented | implemented | implemented | evidence_required | evidence_required | Ledger/Platform; `services/ledger/internal/worker/outbox_relay_test.go`, `services/ledger/internal/ledger/outbox_backoff_contract_test.go`, `services/ledger/internal/ledger/schema_contract_test.go`, `docs/operations/runbooks/outbox-backlog-runbook.md`, `scripts/business-e2e.sh`, `scripts/chaos-test.sh` (scenarios 2 and 23) |
| Auth and KYC | implemented | implemented | implemented | implemented | implemented | partially_implemented | evidence_required | evidence_required | Auth; `services/auth/internal/auth/kyc.go`, `services/auth/internal/worker/expiry_test.go`, `services/auth/internal/auth/kyc_integration_test.go`, `scripts/business-e2e.sh`, `scripts/privacy-e2e-host.sh`, `scripts/chaos-test.sh` (privacy scenarios 15–20) |
| Container/Kubernetes deployment | implemented | not_applicable | not_applicable | implemented | partially_implemented | not_started | evidence_required | evidence_required | Platform; `Dockerfile`, `docker-compose.yml`, `scripts/smoke-container.sh`, `deploy/helm/seev`, `Makefile`, `deploy/kubernetes/scripts/bootstrap-local.sh`, `deploy/kubernetes/scripts/smoke.sh`, `.github/workflows/kubernetes-validate.yml`; Makefile targets platform-integration-check and k8s-integration provide the container/Helm contract and disposable kind bootstrap/smoke paths; this review did not retain a Kubernetes runtime report |
| IaC and workload identity | implemented | not_applicable | not_applicable | implemented | not_started | not_started | evidence_required | evidence_required | Platform; `deploy/terraform`, `scripts/ci/check-iac.sh`, `scripts/ci/check-platform-integration.sh`, `deploy/helm/seev/templates/serviceaccounts.yaml`, `.github/workflows/kubernetes-validate.yml`; backend-free Terraform validation and rendered AWS/GCP identity contracts are implemented, while cloud apply remains an external authorization gate |
| Supply-chain controls | implemented | not_applicable | not_applicable | implemented | partially_implemented | not_applicable | evidence_required | evidence_required | Platform/Security; `scripts/ci/supply-chain-check.sh`, `scripts/ci/verify-supply-chain-evidence.sh`, `.github/workflows/ci.yml`, `.github/workflows/release-provenance.yml`, `docs/acceptance/supply-chain.md`; protected-registry and admission evidence remain external |
| DR and restore | implemented | not_applicable | implemented | implemented | partially_implemented | partially_implemented | evidence_required | evidence_required | Platform; `Makefile`, `scripts/dr-drill.sh`, `.github/workflows/dr-drill.yml`, `operations/recovery/drverify`, `operations/recovery/drreseed`, `docs/operations/backup-restore-evidence.md`; Makefile target dr-integration runs the disposable latest/PITR restore, cross-database verification, reseed, security fence, and post-restore smoke; retained production-shaped evidence remains external |

## Status interpretation

The matrix intentionally keeps E2E/chaos and live acceptance separate from
repository integration completion:

- repository integration can be complete while E2E or chaos coverage is still
  `partially_implemented` or `not_started`;
- `evidence_required` means that source code, CI configuration, or a local
  mock cannot replace a live database/cloud/vendor/registry/production-shaped
  run;
- a release cannot be marked runtime-accepted or production-ready until the
  corresponding retained evidence and owner sign-off exist.
