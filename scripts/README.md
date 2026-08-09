# scripts/ — Operational Index

All scripts use `ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"` so they
can be invoked from any directory. Compatibility shims at the top level forward to the
actual implementation under the category subdirectory, so existing Makefile targets and
muscle memory continue to work.

## Directory layout

```
scripts/
├── lib.sh              Compatibility aggregator — sources all lib/* sub-libraries
├── lib/                Focused library modules (sourced by lib.sh)
│   ├── core.sh         log/ok/fail, require_command, assert_test_harness_environment
│   ├── compose.sh      Docker Compose container resolution and health-polling
│   ├── postgres.sh     psql_exec, detect_db_port, ensure_service_dbs, ensure_app_role
│   ├── migrations.sh   fail-closed _apply_service_migrations, apply_migrations
│   ├── http.sh         curl_internal (mTLS), json_get, json_get_required, json_field
│   ├── fixtures.sh     provision_hold_account, fund_user, KYC helpers, await_* pollers
│   ├── assertions.sh   assert_ledger_balanced, assert_no_inconsistent_projections, etc.
│   ├── backup.sh       pgbackrest_preflight, pgbackrest_compose_exec, pgbackrest_oneshot
│   ├── services.sh     (future) service build/start/stop helpers
│   └── certs.sh        (future) mTLS certificate generation
├── ci/                 CI gates (non-interactive, run by GitHub Actions)
├── ops/                Operational scripts — run by an operator against live environments
├── test/               End-to-end test scripts — run against a local dev stack
├── chaos/              Chaos, DR, and reliability scripts
├── load/               Load testing scripts
└── deploy/             Deployment tooling scripts
```

## ops/ — Operational scripts

| Script | Purpose |
|--------|---------|
| `ops/migrate.sh` | Apply or roll back database migrations (`SERVICE=ledger make migrate-up`) |
| `ops/backup-manifest.sh` | Verify all nine service databases are present in the pgBackRest stanza |
| `ops/backup-checksums-enable.sh` | Enable page-level checksums on a Postgres cluster (requires restart) |
| `ops/restore-cluster.sh` | Full-cluster point-in-time restore from pgBackRest backup |
| `ops/pgbackrest.sh` | pgBackRest wrapper — read passphrase inside container, never in host argv |
| `ops/rebuild-projection.sh` | Re-project ledger account balances from ledger_entries |
| `ops/grant-app-role.sh` | Grant the app_service role to the application user |
| `ops/vault-seed.sh` | Seed Vault with dev/test secrets |
| `ops/product-assurance.sh` | Operator CLI for Plan 48 assurance surfaces (findings, pause, resume) |

### Safety contracts

- **`migrate.sh down`** requires `SEEV_MIGRATE_DOWN_ACK=<env>:<service>`; production rollback additionally requires `SEEV_MIGRATE_DOWN_PRODUCTION_OVERRIDE=1`.
- **`backup-checksums-enable.sh`** requires `SEEV_CHECKSUM_ACK=<compose_project>` and restarts Postgres under a trap.
- **`restore-cluster.sh`** reads `PGBACKREST_PASSPHRASE_FILE`; the passphrase is consumed inside the container — never in host `docker run -e`.

## test/ — End-to-end test scripts

Run against a running local dev stack (see `make start` / `make ensure-up`).

| Script | Purpose |
|--------|---------|
| `test/smoke-test.sh` | Full smoke suite — ledger, topup, payout, withdraw |
| `test/business-e2e.sh` | Business flows — topup, payout, routing, KYC, quotes |
| `test/admin-e2e.sh` | Admin BFF surfaces — notification templates, gateway channels |
| `test/capability-e2e.sh` | Product capability flows — bulk disbursement, FX, disputes |
| `test/merchant-e2e.sh` | B2B merchant (payin/transfer) flows |
| `test/interest-period-e2e.sh` | Interest accrual and period close |
| `test/migration-balance-v2-e2e.sh` | Balance-v2 migration and repair flows |
| `test/multi-currency-e2e.sh` | Multi-currency account and conversion flows |
| `test/privacy-e2e.sh` | GDPR/privacy request and account closure flows |
| `test/topup-fee-e2e.sh` | Top-up fee rule configuration and enforcement |
| `test/schedule-policy-e2e.sh` | Scheduled payment policy flows |
| `test/platform-e2e.sh` | Platform-level E2E (Kubernetes / infra smoke) |

## chaos/ — Chaos, DR, and reliability scripts

| Script | Purpose |
|--------|---------|
| `chaos/chaos-test.sh` | Full chaos suite — 20+ scenarios covering network, infra, and business faults |
| `chaos/dr-drill.sh` | Disaster-recovery drill — backup → destroy → restore → validate |
| `chaos/dr-e2e.sh` | DR end-to-end — fixture write, full restore, assertion |
| `chaos/rotation-drill.sh` | Secret and key rotation drill |
| `chaos/capability-chaos.sh` | Capability-layer chaos (application and platform modes) |
| `chaos/vendor-boundary-chaos.sh` | Vendor API boundary failure and retry chaos |
| `chaos/smoke-container.sh` | Smoke suite against pre-built Docker images (CI, --no-build) |
| `chaos/runtime-acceptance.sh` | Runtime acceptance gate (Kubernetes) |
| `chaos/post-restore-security.sh` | Post-restore security checks |
| `chaos/privacy-e2e-host.sh` | Host-mode privacy E2E |
| `chaos/privacy-export.sh` | GDPR export pipeline E2E |

## load/ — Load testing scripts

Require `SEEV_LOAD_ACK=disposable-only` to protect non-disposable environments.

| Script | Purpose |
|--------|---------|
| `load/load-seed.sh` | Seed a load-test volume with users and balances |
| `load/load-snapshot.sh` | Snapshot or restore a load-test volume |
| `load/load-test.sh` | Run the k6 load suite (smoke / run / clean) |
| `load/load-capacity.sh` | Run the capacity baseline |
| `load/load-report-check.sh` | Validate a load report against capacity targets |

## deploy/ — Deployment tooling

| Script | Purpose |
|--------|---------|
| `deploy/deployment-inventory-check.sh` | Verify running deployment matches inventory baseline |
| `deploy/deployment-inventory-generate.sh` | Generate a deployment inventory snapshot |
| `deploy/deployment-network-probe.sh` | Probe inter-service connectivity from a running cluster |
| `deploy/deployment-resource-sample.sh` | Sample CPU/memory resource usage from a running cluster |

## ci/ — CI gates

Scripts under `ci/` are run by GitHub Actions; they are not intended for direct operator use. See `.github/workflows/ci.yml` for the full pipeline.

## Common environment variables

| Variable | Used by | Purpose |
|----------|---------|---------|
| `APP_ENV` | lib/core.sh | Must be `development`, `test`, `ci`, `drill`, `load`, or `gameday` |
| `WORK_DIR` | lib/core.sh | Working directory for binaries and logs; must be under a temp path |
| `POSTGRES_CONTAINER` | lib/postgres.sh | Name of the running Postgres container |
| `CERT_DIR` | lib/http.sh | Directory containing `ca.pem`, `dev-operator.pem`, `dev-operator-key.pem` |
| `PGBACKREST_PASSPHRASE_FILE` | lib/backup.sh | Path to a file containing the pgBackRest repository passphrase |
| `SEEV_MIGRATE_DOWN_ACK` | ops/migrate.sh | Safety acknowledgement for `migrate.sh down` (`<env>:<service>`) |
| `SEEV_LOAD_ACK` | load/\*.sh | Must be `disposable-only` to run load scripts |
| `KEEP_WORK_DIR` | lib.sh cleanup | Set to `1` to retain WORK_DIR logs after script exit |

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Assertion or runtime failure |
| `2` | Bad arguments / missing required input |
