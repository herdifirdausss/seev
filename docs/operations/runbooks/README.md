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
| A pkg/cryptox encryption key needs rotating or is suspected compromised | [Cryptox key rotation](cryptox-key-rotation.md) | Never retire an old version until no row still references it |
| Payin/Payout and Ledger records disagree | [Product assurance](product-assurance.md) | Pause intake only through the controlled workflow |
| A KYC or screening queue cannot recover | [Compliance](compliance-a4.md) | Preserve the failed record and audit trail |
| One side of an FX pair is missing | [FX position](fx-position.md) | Never fabricate the missing leg |
| A restore exercise or disaster recovery is required | [DR restore](dr-restore-drill.md) | Restore into the documented isolated target |
| Scheduled backup hasn't succeeded recently | [Backup failure](backup-failure.md) | Confirm the previous chain is still intact before retrying |
| WAL archiving has stalled | [WAL archive lag](wal-archive-lag.md) | Force a segment switch to confirm the fix; don't wait it out |
| A backup fails its own repository check | [Repository corruption](repository-corruption.md) | Never expire data while a check is failing |
| A retention hold, purge, export, or account-closure request is stuck or failing | [Data lifecycle and privacy](data-lifecycle-privacy.md) | Never manually purge, decrypt, or flip a closure/export status to "unstick" it |

| Runbook | Covers |
|---|---|
| [cert-rotation.md](cert-rotation.md) | Rotating the internal mini-CA and every service's mTLS leaf certificate |
| [cryptox-key-rotation.md](cryptox-key-rotation.md) | Rotating the shared pkg/cryptox encryption key ring (expand/backfill/contract) |
| [handshake-failure-response.md](handshake-failure-response.md) | Responding to a rise in mTLS handshake failures (`tlsx_handshake_failures_total`) |
| [ledger-integrity-alert.md](ledger-integrity-alert.md) | Responding to a trial-balance or projection-audit discrepancy alert |
| [dr-restore-drill.md](dr-restore-drill.md) | Restoring all eight service databases from backup (latest or PITR) and proving it's safe to serve traffic again |
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

Each runbook is self-contained: what triggers it, what to check, and the
exact commands to run — no need to read another document first to act on
one. See [Operations](../README.md) for the tooling these
runbooks lean on (`scripts/`, Compose, CI) and
[docs/security/threat-model.md](../../security/threat-model.md) for the
security findings some of these (cert-rotation,
handshake-failure-response) trace back to.
