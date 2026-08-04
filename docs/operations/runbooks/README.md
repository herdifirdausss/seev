# Runbooks

> **Status: Current. Audience: operators.** A runbook is a checklist for a
> known operational problem. If a command's target or effect is unclear, stop
> and escalate instead of experimenting on financial data.

Operational recovery procedures — what to do when a specific alert fires
or a specific incident happens. These are written for whoever is on call,
not as architecture reference; see [Architecture](../../reference/architecture.md)
and [Services](../../reference/services.md) for that.

## Choose by symptom

| What you observe | Start here | First safety rule |
|---|---|---|
| Trial balance or projection disagreement | [Ledger integrity](ledger-integrity-alert.md) | Do not edit or delete ledger entries |
| Seev and an external settlement report disagree | [Reconciliation](reconciliation.md) | Confirm the external evidence before resolving |
| Internal TLS handshakes suddenly fail | [Handshake failures](handshake-failure-response.md) | Identify CA versus identity failure before rotating |
| Certificates are near expiry or intentionally replaced | [Certificate rotation](cert-rotation.md) | Generate and verify before removing old material |
| A internal/platform/security/crypto encryption key needs rotating or is suspected compromised | [Cryptox key rotation](cryptox-key-rotation.md) | Never retire an old version until no row still references it |
| Payin/Payout and Ledger records disagree | [Product assurance](product-assurance.md) | Pause intake only through the controlled workflow |
| A KYC or screening queue cannot recover | [Compliance](compliance-a4.md) | Preserve the failed record and audit trail |
| One side of an FX pair is missing | [FX position](fx-position.md) | Never fabricate the missing leg |
| A restore exercise or disaster recovery is required | [DR restore](dr-restore-drill.md) | Restore into the documented isolated target |
| Scheduled backup hasn't succeeded recently | [Backup failure](backup-failure.md) | Confirm the previous chain is still intact before retrying |
| WAL archiving has stalled | [WAL archive lag](wal-archive-lag.md) | Force a segment switch to confirm the fix; don't wait it out |
| A backup fails its own repository check | [Repository corruption](repository-corruption.md) | Never expire data while a check is failing |
| A retention hold, purge, export, or account-closure request is stuck or failing | [Data lifecycle and privacy](data-lifecycle-privacy.md) | Never manually purge, decrypt, or flip a closure/export status to "unstick" it |
| A contract or schema compatibility gate fails | [API contract evolution](api-contract-evolution.md) | Keep the old consumer boundary live until acknowledgement and zero-use gates pass |
| A B0 load run saturates, aborts, or reports an integrity failure | [B0 load and capacity](b0-load-capacity.md) | Stop offered work, drain, and verify money state before cleanup |
| A vendor callback is rejected, duplicated, or cannot reach its owner | [VendorService boundary](vendor-service-boundary.md) | Preserve the VendorService inbox and do not replay raw callbacks manually |
| A merchant reports a leaked API key | [Merchant API key compromise](merchant-api-key-compromise.md) | Revoke immediately; never resend a plaintext key over an insecure channel |
| A single merchant tenant needs to stop transacting now | [Merchant tenant suspension](merchant-tenant-suspension.md) | Suspension is per-tenant; use the global kill switch only for a platform-wide incident |
| Merchant requests are failing 503 and Redis is unreachable | [Merchant quota backend outage](merchant-quota-backend-outage.md) | Writes fail closed by default; do not flip fail-open mid-incident without authority |
| A merchant idempotency record is stuck in `processing` | [Merchant idempotency stuck](merchant-idempotency-stuck.md) | Never manually flip a record's state; let the merchant retry the same key |
| Merchant webhook deliveries are backing up or dead-lettering | [Merchant webhook backlog](merchant-webhook-backlog.md) | Diagnose the endpoint before replaying; replay reuses the original event id |
| A merchant reports a leaked webhook signing secret | [Merchant webhook secret compromise](merchant-webhook-secret-compromise.md) | In-flight retries keep using the old secret until they drain |
| Ledger/Payin/Payout is unreachable from merchant traffic | [Merchant owner-service outage](merchant-owner-service-outage.md) | Merchants must retry with the SAME idempotency key, never a new one |
| Analytics CDC or OLAP ingestion is stalled | [Analytics ingestion](analytics-clickhouse-ingestion-stalled.md) | Keep the OLTP source authoritative; do not repair facts in the warehouse |
| Analytics replication, connector, or dbt health is degraded | [Analytics connector](analytics-connector-failed.md), [source WAL](analytics-source-wal-pressure.md), or [dbt failure](analytics-dbt-failure.md) | Check lag and retention before restarting or rebuilding |
| A notification queue, template, provider, or recipient flow is failing | [Notification provider](notification-provider-outage.md), [delivery backlog](notification-email-backlog.md), or [template incident](notification-template-incident.md) | Preserve the durable notification state; do not send outside the delivery ledger |
| A ledger schedule, dispute deadline, or outbox row is stuck | [Stuck state](stuck-state-runbook.md) | Do not replay until the current state, lease, and idempotency key are recorded |

| Runbook | Covers |
|---|---|
| [cert-rotation.md](cert-rotation.md) | Rotating the internal mini-CA and every service's mTLS leaf certificate |
| [cryptox-key-rotation.md](cryptox-key-rotation.md) | Rotating the shared internal/platform/security/crypto encryption key ring (expand/backfill/contract) |
| [handshake-failure-response.md](handshake-failure-response.md) | Responding to a rise in mTLS handshake failures (`tlsx_handshake_failures_total`) |
| [ledger-integrity-alert.md](ledger-integrity-alert.md) | Responding to a trial-balance or projection-audit discrepancy alert |
| [dr-restore-drill.md](dr-restore-drill.md) | Restoring all nine core service databases from backup (latest or PITR) and proving it's safe to serve traffic again |
| [backup-failure.md](backup-failure.md) | Recovering from a missed or failed scheduled full/differential backup |
| [wal-archive-lag.md](wal-archive-lag.md) | Recovering from stalled continuous WAL archiving before the RPO budget is exceeded |
| [repository-corruption.md](repository-corruption.md) | Handling a pgBackRest repository check failure without losing the other retained chain |
| [reconciliation.md](reconciliation.md) | The daily external settlement reconciliation flow: import, match, resolve |
| [regulatory-reporting.md](regulatory-reporting.md) | Pulling fund-position, transaction-mutation, and reconciliation-summary reports |
| [compliance-a4.md](compliance-a4.md) | KYC apply-retry dead-letter recovery and fraud screening-event spill recovery |
| [product-assurance.md](product-assurance.md) | Operating Assurance's findings lifecycle and the emergency intake pause/resume controls |
| [fx-position.md](fx-position.md) | Handling an incomplete `fx_out`/`fx_in` currency-conversion pair |
| [vault-seed.md](vault-seed.md) | Seeding the local dev-mode Vault after a restart |
| [data-lifecycle-privacy.md](data-lifecycle-privacy.md) | Active retention holds, failing purge/redact classes, failed exports, stuck/dead-lettered account closures, and privacy-track key-version mismatches |
| [api-contract-evolution.md](api-contract-evolution.md) | HTTP, event, and protobuf compatibility failures and safe retirement |
| [b0-load-capacity.md](b0-load-capacity.md) | Disposable load preparation, abort/drain, bottleneck diagnosis, integrity failure, and retention |
| [vendor-service-boundary.md](vendor-service-boundary.md) | Vendor callback source policy, inbox replay, normalized owner delivery, and outbound mTLS boundary |
| [merchant-api-key-compromise.md](merchant-api-key-compromise.md) | Revoking a leaked merchant API key and issuing a replacement |
| [merchant-tenant-suspension.md](merchant-tenant-suspension.md) | Suspending/reactivating a single merchant tenant, and how it differs from the global kill switch |
| [merchant-quota-backend-outage.md](merchant-quota-backend-outage.md) | Redis-backed quota enforcement's fail-closed/fail-open behavior during an outage |
| [merchant-idempotency-stuck.md](merchant-idempotency-stuck.md) | Diagnosing and (self-)recovering merchant idempotency records stuck under an expired lease |
| [merchant-webhook-backlog.md](merchant-webhook-backlog.md) | Diagnosing webhook delivery backlog/dead-letters and replaying eligible deliveries |
| [merchant-webhook-secret-compromise.md](merchant-webhook-secret-compromise.md) | Rotating a merchant webhook endpoint's signing secret and handling the in-flight overlap window |
| [merchant-owner-service-outage.md](merchant-owner-service-outage.md) | Merchant-facing impact of a Ledger/Payin/Payout outage and why same-key retries are safe |
| [database-failover-runbook.md](database-failover-runbook.md) | Re-establishing database connectivity and replaying durable work after failover |
| [outbox-backlog-runbook.md](outbox-backlog-runbook.md) | Investigating old pending/failed outbox events and safe replay |
| [payout-unknown-state-runbook.md](payout-unknown-state-runbook.md) | Recovering a payout whose provider result is unknown without duplicate submission |
| [reconciliation-mismatch-runbook.md](reconciliation-mismatch-runbook.md) | Quarantining and correcting a settlement mismatch through maker-checker |
| [stuck-state-runbook.md](stuck-state-runbook.md) | Triage for scheduled, dispute, and outbox stuck-state gauges |
| [analytics-*.md](.) | C2 CDC, OLAP ingestion, dbt, reconciliation, rebuild, and sensitive-data incident procedures |
| [notification-*.md](.) | C3 notification event, template, provider, channel, backlog, and recipient-protection procedures |

Each runbook is self-contained: what triggers it, what to check, and the
exact commands to run — no need to read another document first to act on
one. See [Operations](../README.md) for the tooling these
runbooks lean on (`scripts/`, Compose, CI) and
[docs/security/threat-model.md](../../security/threat-model.md) for the
security findings some of these (cert-rotation,
handshake-failure-response) trace back to.
