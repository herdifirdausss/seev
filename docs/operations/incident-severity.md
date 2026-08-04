# Incident severity and escalation

| Severity | Definition | Initial response | Examples |
|---|---|---|---|
| SEV-1 | Active or likely financial loss, duplicate settlement, credential compromise, or broad inability to establish ledger truth | page immediately; stop affected intake; incident commander and security lead | duplicate payout risk, unknown vendor outcomes beyond SLA, leaked production secret |
| SEV-2 | Material correctness, compliance, or availability degradation with bounded scope | page on-call; owner responds within 15 minutes | outbox backlog over critical threshold, reconciliation mismatch, KYC propagation failure |
| SEV-3 | Degraded non-critical capability with safe workaround | ticket/on-call response in business hours | delayed report, isolated rejected schedule, non-critical dashboard gap |
| SEV-4 | Cosmetic/documentation or planned improvement | normal backlog | copy, dashboard label, low-risk tooling issue |

## Escalation rules

- Escalate to SEV-1 when the team cannot prove whether money moved once.
- Freeze affected provider/tenant flows before attempting a repair.
- Preserve logs, event payload hashes, database identifiers, and correlation IDs.
- Never repair financial history with an ad-hoc SQL update.
- Close only after the runbook action, reconciliation, and evidence bundle are
  complete.
