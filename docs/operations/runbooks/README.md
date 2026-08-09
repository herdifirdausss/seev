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
| A `internal/platform/security/crypto` encryption key needs rotating | [Cryptox key rotation](cryptox-key-rotation.md) | Never retire an old version until no row still references it |
| Payin/Payout and Ledger records disagree | [Product assurance](product-assurance.md) | Pause intake only through the controlled workflow |
| A KYC or screening queue cannot recover | [Compliance](compliance-a4.md) | Preserve the failed record and audit trail |
| One side of an FX pair is missing | [FX position](fx-position.md) | Never fabricate the missing leg |
| A restore exercise or disaster recovery is required | [DR restore](dr-restore-drill.md) | Restore into the documented isolated target |
| Scheduled backup hasn't succeeded recently | [Backup failure](backup-failure.md) | Confirm the previous chain is still intact before retrying |
| WAL archiving has stalled | [WAL archive lag](wal-archive-lag.md) | Force a segment switch to confirm the fix; don't wait it out |
| A backup fails its own repository check | [Repository corruption](repository-corruption.md) | Never expire data while a check is failing |
| A retention hold, purge, export, or closure request is stuck | [Data lifecycle and privacy](data-lifecycle-privacy.md) | Never manually purge, decrypt, or flip a closure status to "unstick" it |
| A contract or schema compatibility gate fails | [API contract evolution](api-contract-evolution.md) | Keep the old consumer boundary live until acknowledgement and zero-use gates pass |
| A B0 load run saturates, aborts, or reports an integrity failure | [B0 load and capacity](b0-load-capacity.md) | Stop offered work, drain, and verify money state before cleanup |
| A vendor callback is rejected, duplicated, or cannot reach its owner | [VendorService boundary](vendor-service-boundary.md) | Preserve the VendorService inbox; do not replay raw callbacks manually |
| A ledger schedule, dispute deadline, or outbox row is stuck | [Stuck state](stuck-state-runbook.md) | Do not replay until the current state, lease, and idempotency key are recorded |
| **Merchant:** leaked API key | [merchants.md § API key compromise](merchants.md#api-key-compromise) | Revoke immediately; never resend a plaintext key over an insecure channel |
| **Merchant:** single tenant needs to stop transacting now | [merchants.md § Tenant suspension](merchants.md#tenant-suspension) | Suspension is per-tenant; use the global kill switch only for a platform-wide incident |
| **Merchant:** requests failing 503 / Redis unreachable | [merchants.md § Quota backend outage](merchants.md#quota-backend-outage) | Writes fail closed by default; do not flip fail-open mid-incident without authority |
| **Merchant:** idempotency record stuck in `processing` | [merchants.md § Idempotency stuck](merchants.md#idempotency-stuck) | Never manually flip a record's state; let the merchant retry the same key |
| **Merchant:** webhook deliveries backing up or dead-lettering | [merchants.md § Webhook backlog](merchants.md#webhook-backlog) | Diagnose the endpoint before replaying; replay reuses the original event id |
| **Merchant:** leaked webhook signing secret | [merchants.md § Webhook secret compromise](merchants.md#webhook-secret-compromise) | In-flight retries keep using the old secret until they drain |
| **Merchant:** Ledger/Payin/Payout unreachable from merchant path | [merchants.md § Owner-service outage](merchants.md#owner-service-outage) | Merchants must retry with the SAME idempotency key, never a new one |
| **Analytics:** CDC or OLAP ingestion is stalled | [analytics.md § ClickHouse ingestion stalled](analytics.md#clickhouse-ingestion-stalled) | Keep the OLTP source authoritative; do not repair facts in the warehouse |
| **Analytics:** connector failed or replication slot lag growing | [analytics.md § Connector failed](analytics.md#connector-failed) | Check lag and retention before restarting or rebuilding |
| **Analytics:** dbt failure | [analytics.md § dbt failure](analytics.md#dbt-failure) | Test both full-refresh and incremental paths |
| **Analytics:** schema change blocked or prohibited column detected | [analytics.md § Schema change](analytics.md#schema-change) | Pause the connector before reviewing; additive nullable changes only |
| **Analytics:** sensitive data in warehouse | [analytics.md § Sensitive data incident](analytics.md#sensitive-data-incident) | Stop downstream exposure before investigating |
| **Analytics:** Redpanda outage | [analytics.md § Redpanda outage](analytics.md#redpanda-outage) | Never change RabbitMQ topology as a recovery action |
| **Analytics:** full rebuild required | [analytics.md § Full rebuild](analytics.md#full-rebuild) | `ANALYTICS_CONFIRM_RESET=analytics-only` is required to clear offset storage |
| **Notification:** event DLQ growing | [notifications.md § Event DLQ](notifications.md#event-dlq) | Preserve the durable notification state; do not send outside the delivery ledger |
| **Notification:** provider outage (SMTP/push) | [notifications.md § Provider outage](notifications.md#provider-outage) | Pause channel and preserve rendered snapshots before replay |
| **Notification:** email or push backlog growing | [notifications.md § Email backlog](notifications.md#email-backlog) / [Push backlog](notifications.md#push-backlog) | Do not delete pending deliveries |
| **Notification:** template missing or incorrect | [notifications.md § Template missing](notifications.md#template-missing) / [Template incident](notifications.md#template-incident) | Do not edit an active version in place |
| **Notification:** recipient data exposure | [notifications.md § Recipient data exposure](notifications.md#recipient-data-exposure) | Treat as a security incident; pause the channel immediately |
| **Migration:** balance API returns wrong values after read ramp | [migrations.md § Instant read rollback](migrations.md#instant-read-rollback) | Set `read_percentage` to 0 first, investigate second |
| **Migration:** postings returning 5xx at ShadowRead or later | [migrations.md § Strict dual-write failure](migrations.md#strict-dual-write-failure) | Confirm the target write is the cause before switching mode |
| **Migration:** open mismatches or `checksum_mismatch` alert | [migrations.md § Shadow mismatch](migrations.md#shadow-mismatch) | Never approve repair for `shared_corruption` classification |
| **Migration:** stuck in `backfilling` with no progress | [migrations.md § Backfill stalled](migrations.md#backfill-stalled) | Wait for lease expiry; do not truncate the target table |

## All runbooks

| Runbook | Covers |
|---|---|
| [ledger-integrity-alert.md](ledger-integrity-alert.md) | Trial-balance or projection-audit discrepancy |
| [reconciliation.md](reconciliation.md) | Daily external settlement: import, match, resolve |
| [reconciliation-mismatch-runbook.md](reconciliation-mismatch-runbook.md) | Quarantining and correcting a settlement mismatch through maker-checker |
| [cert-rotation.md](cert-rotation.md) | Rotating the internal mini-CA and every service's mTLS leaf certificate |
| [cryptox-key-rotation.md](cryptox-key-rotation.md) | Rotating the shared crypto key ring (expand/backfill/contract) |
| [handshake-failure-response.md](handshake-failure-response.md) | Rise in mTLS handshake failures |
| [product-assurance.md](product-assurance.md) | Assurance findings lifecycle and emergency intake pause/resume |
| [compliance-a4.md](compliance-a4.md) | KYC apply-retry dead-letter recovery and fraud screening-event spill |
| [fx-position.md](fx-position.md) | Incomplete `fx_out`/`fx_in` currency-conversion pair |
| [dr-restore-drill.md](dr-restore-drill.md) | Restoring all nine service databases from backup (latest or PITR) |
| [backup-failure.md](backup-failure.md) | Missed or failed scheduled backup |
| [wal-archive-lag.md](wal-archive-lag.md) | Stalled WAL archiving before RPO budget is exceeded |
| [repository-corruption.md](repository-corruption.md) | pgBackRest repository check failure |
| [database-failover-runbook.md](database-failover-runbook.md) | Re-establishing database connectivity and replaying durable work after failover |
| [outbox-backlog-runbook.md](outbox-backlog-runbook.md) | Old pending/failed outbox events and safe replay |
| [payout-unknown-state-runbook.md](payout-unknown-state-runbook.md) | Payout whose provider result is unknown without duplicate submission |
| [stuck-state-runbook.md](stuck-state-runbook.md) | Scheduled, dispute, and outbox stuck-state gauges |
| [data-lifecycle-privacy.md](data-lifecycle-privacy.md) | Retention holds, failing purge/redact, stuck account closures |
| [api-contract-evolution.md](api-contract-evolution.md) | HTTP, event, and protobuf compatibility failures and safe retirement |
| [b0-load-capacity.md](b0-load-capacity.md) | Load preparation, abort/drain, bottleneck diagnosis, integrity failure |
| [vendor-service-boundary.md](vendor-service-boundary.md) | Vendor callback source policy, inbox replay, normalized owner delivery |
| [regulatory-reporting.md](regulatory-reporting.md) | Fund-position, transaction-mutation, and reconciliation-summary reports |
| [vault-seed.md](vault-seed.md) | Seeding the local dev-mode Vault after a restart |
| [merchants.md](merchants.md) | API key compromise, tenant suspension, idempotency, quota, webhooks |
| [analytics.md](analytics.md) | CDC ingestion, dbt, Redpanda, ClickHouse, schema change, sensitive-data incident |
| [notifications.md](notifications.md) | Event DLQ, provider, templates, channel pause, backlog, recipient data |
| [migrations.md](migrations.md) | Backfill stall, instant read rollback, shadow mismatch, strict dual-write failure |

Each runbook is self-contained: what triggers it, what to check, and the
exact commands to run. See [Operations](../README.md) for the tooling these
runbooks lean on (`scripts/`, Compose, CI) and
[docs/security/threat-model.md](../../security/threat-model.md) for the
security findings some of these (cert-rotation, handshake-failure-response) trace back to.
