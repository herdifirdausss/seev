# Database failover runbook

1. Declare the incident and identify the writer, replica/standby, migration
   version, and last known committed transaction/outbox position.
2. Stop money-moving intake if writer health or commit visibility is uncertain.
3. Promote the approved standby using the managed-database procedure; do not
   issue ad-hoc writes while split-brain is possible.
4. Validate schema version, RLS/roles, sequences, advisory locks, and a ledger
   balance sample before reopening traffic.
5. Replay durable outbox/schedule/recovery work using original idempotency
   scopes and keys. Run reconciliation.
6. Record RTO/RPO, missing/duplicated request analysis, and follow-up actions.
