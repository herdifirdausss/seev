# Money-flow SLOs and alerts

These are initial operational objectives. Production values must be approved
with measured traffic and vendor SLAs before they become capacity claims.

| Signal | Objective/threshold | Alert | Runbook |
|---|---|---|---|
| Ledger post availability | 99.9% successful non-business requests/month | 5m burn / 1h burn | `database-failover-runbook.md` |
| Ledger post latency | p95 < 500ms, p99 < 1s excluding vendor calls | 5m p95 breach | incident severity + ledger integrity |
| Outbox oldest age | warn 5m, critical 15m | pending count and oldest age | `outbox-backlog-runbook.md` |
| Payout unknown age | warn at provider SLA, critical at 2× SLA | unknown count/oldest age | `payout-unknown-state-runbook.md` |
| Reconciliation mismatch age | no unresolved material mismatch past settlement SLA | age and amount threshold | `reconciliation-mismatch-runbook.md` |
| Scheduled processing age | due occurrence is claimed within 2 worker intervals | due/processing age | scheduled acceptance/runbook |
| Dispute deadline | no unprocessed due dispute | due count | dispute acceptance |

The repository alert rules are in
[`deploy/observability/prometheus/rules/improvement.yml`](../../deploy/observability/prometheus/rules/improvement.yml).
Every alert must link to an owner, a dashboard, and an executable runbook.
