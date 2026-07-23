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
| Payin/Payout and Ledger records disagree | [Product assurance](product-assurance.md) | Pause intake only through the controlled workflow |
| A KYC or screening queue cannot recover | [Compliance](compliance-a4.md) | Preserve the failed record and audit trail |
| One side of an FX pair is missing | [FX position](fx-position.md) | Never fabricate the missing leg |
| A restore exercise or disaster recovery is required | [DR restore](dr-restore-drill.md) | Restore into the documented isolated target |

| Runbook | Covers |
|---|---|
| [cert-rotation.md](cert-rotation.md) | Rotating the internal mini-CA and every service's mTLS leaf certificate |
| [handshake-failure-response.md](handshake-failure-response.md) | Responding to a rise in mTLS handshake failures (`tlsx_handshake_failures_total`) |
| [ledger-integrity-alert.md](ledger-integrity-alert.md) | Responding to a trial-balance or projection-audit discrepancy alert |
| [dr-restore-drill.md](dr-restore-drill.md) | Restoring the ledger database from backup and proving it's usable again |
| [reconciliation.md](reconciliation.md) | The daily external settlement reconciliation flow: import, match, resolve |
| [regulatory-reporting.md](regulatory-reporting.md) | Pulling fund-position, transaction-mutation, and reconciliation-summary reports |
| [compliance-a4.md](compliance-a4.md) | KYC apply-retry dead-letter recovery and fraud screening-event spill recovery |
| [product-assurance.md](product-assurance.md) | Operating Assurance's findings lifecycle and the emergency intake pause/resume controls |
| [fx-position.md](fx-position.md) | Handling an incomplete `fx_out`/`fx_in` currency-conversion pair |
| [vault-seed.md](vault-seed.md) | Seeding the local dev-mode Vault after a restart |

Each runbook is self-contained: what triggers it, what to check, and the
exact commands to run — no need to read another document first to act on
one. See [Operations](../README.md) for the tooling these
runbooks lean on (`scripts/`, Compose, CI) and
[docs/security/threat-model.md](../../security/threat-model.md) for the
security findings some of these (cert-rotation,
handshake-failure-response) trace back to.
