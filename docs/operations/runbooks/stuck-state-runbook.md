# Stuck-state scanner runbook

Use this runbook for `SeevLedgerStuckMoneyState` or an unexplained rise in
`ledger_stuck_state_count`.

1. Record the alert timestamp, state label, deployment revision, and trace or
   correlation IDs. Do not mutate ledger rows directly.
2. Inspect the ledger dashboard and the durable row age for the affected
   schedule occurrence, dispute, or outbox event.
3. Check worker health, database lock/deadlock metrics, broker connectivity,
   and recent rollout/termination events.
4. For outbox rows, follow
   [`outbox-backlog-runbook.md`](outbox-backlog-runbook.md). For dispute
   deadlines, allow the expiry worker to claim the row or use the audited
   operator action after confirming its current state.
5. Replay only with the original idempotency key and after confirming that no
   terminal financial outcome already exists. A duplicate callback is a
   successful no-op, not a reason to post again.
6. Run ledger balance verification and reconciliation after recovery. Escalate
   as SEV-1 if an outcome is ambiguous or a balance mismatch appears.

For `SeevMoneyMovementPolicyDenialSpike`, group by `source`, `reason`, and
`tenant` in the audit table, then compare the deployment revision and policy
configuration. Never weaken the policy or bypass the executor to clear the
alert.
