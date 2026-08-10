# Seev Backup & Recovery Ownership

DEPLOY-008: This document is the single authoritative source for which component
owns backup and recovery for each deployment environment.

## Environment Ownership Matrix

| Environment | Component | Owner | Mechanism |
|---|---|---|---|
| local / dev (Compose) | pgBackRest | operations | pgBackRest full+diff+archive, `make backup-full` |
| GCP staging / prod | Cloud SQL | GCP managed | Cloud SQL automated backup + PITR |
| AWS staging / prod | RDS | AWS managed | RDS automated backup + PITR + final snapshot on delete |

## Recovery Policy

| Metric | Target | Enforcing check |
|---|---|---|
| RPO | ≤ 1 hour | pgBackRest WAL archive interval; Cloud SQL/RDS PITR granularity |
| RTO | ≤ 4 hours | DR drill evidence required before staging sign-off |
| Retention (point-in-time) | 7 days minimum | Terraform `backup_retention_period` / `pointInTimeRecovery.enabled` |
| Retention (snapshots) | 30 days minimum | Terraform `final_snapshot_identifier` + lifecycle rules |

## Local / Compose pgBackRest Path

```
make backup-full          # full backup to deploy/backup/repo/
make backup-diff          # incremental since last full
make backup-check         # verify integrity of latest backup set
make dr-e2e               # full latest-restore + PITR drill → reports RPO/RTO
```

Evidence produced by `dr-e2e` is stored in `deploy/backup/dr-evidence/` and
contains: backup completion timestamp, restore elapsed time, measured RPO, RTO
assertion, and application-level business-invariant verification result.

## Cloud Restore Drills

Before production sign-off, run provider-native restores into isolated
**disposable recovery resources** (never into the live database):

**GCP (Cloud SQL):**
```sh
gcloud sql backups restore BACKUP_ID \
  --restore-instance=seev-recovery-test-$(date +%Y%m%d) \
  --backup-instance=seev-ENVIRONMENT
```

**AWS (RDS):**
```sh
aws rds restore-db-instance-to-point-in-time \
  --source-db-instance-identifier seev-ENVIRONMENT \
  --target-db-instance-identifier seev-recovery-test-$(date +%Y%m%d) \
  --restore-time $(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ)
```

After restore, run application-level invariant verification (not only
database availability):
- ledger balance consistency check
- payin/payout idempotency key state
- auth session store health

**Cost leakage prevention:** The operator who starts the recovery resource is
responsible for deleting it within 24 hours. Use a CI job or calendar reminder.

## Cloud Restore Evidence Format

Cloud restore drills must produce evidence in the same JSON shape as the local
pgBackRest drill (`scripts/chaos/dr-drill.sh`), so RPO/RTO can be compared
across environments. Write the report to `deploy/backup/dr-evidence/<env>-<mode>-<date>.json`.

**Required fields (match local drill schema):**

```json
{
  "mode": "latest",
  "disaster_at": "2026-08-10T03:00:00Z",
  "recovered_at": "2026-08-10T03:47:12Z",
  "rpo_seconds": 142,
  "rto_seconds": 2832,
  "rpo_budget_seconds": 3600,
  "rto_budget_seconds": 14400,
  "passed": true,
  "provider": "gcp",
  "environment": "staging",
  "recovery_instance": "seev-recovery-test-20260810",
  "invariants_verified": {
    "ledger_balance_consistent": true,
    "payin_idempotency_keys_intact": true,
    "auth_session_store_healthy": true
  },
  "recovery_instance_deleted_at": "2026-08-10T10:00:00Z",
  "stages": {
    "restore_initiated": "2026-08-10T03:00:00Z",
    "database_available": "2026-08-10T03:42:00Z",
    "application_smoke_passed": "2026-08-10T03:47:12Z"
  }
}
```

**Notes:**
- `rpo_budget_seconds: 3600` (1 hour) for cloud; local drill uses 300s (5 min WAL archive).
- `rto_budget_seconds: 14400` (4 hours) for cloud; local drill uses 1200s (20 min).
- `passed` must be `true` before staging sign-off. A failed drill blocks promotion.
- The evidence file is gitignored — store it as a CI artifact or in the secure doc store.

## Kubernetes Manifests Directory

`manifests/` is intentionally empty. Kubernetes workloads in this repository
use **provider-managed database backup** (Cloud SQL / RDS — see table above).
A Kubernetes CronJob-based backup agent is NOT part of the target architecture.
If that changes, document the scope here before adding manifests, and ensure
it does not duplicate what the managed service already provides.

## Terraform Settings Cross-Reference

Backup configuration in Terraform must satisfy the declared policy:
- `deploy/terraform/aws/dev/main.tf` → `backup_retention_period`, `skip_final_snapshot`
- `deploy/terraform/gcp/dev/main.tf` → `backup_configuration`, `point_in_time_recovery_enabled`

CI check: `scripts/ci/check-platform-integration.sh` validates Terraform settings
against this policy.
