# Outbox backlog runbook

1. Check oldest pending age, pending count, publish error rate, broker health,
   and dead-letter count.
2. If the broker is unavailable, restore broker connectivity without deleting
   outbox rows. The ledger remains the source of truth.
3. If one event is poison, isolate it through the typed dead-letter endpoint;
   inspect payload hash and consumer response.
4. Replay only after the consumer is idempotent and the dependency is healthy.
5. Verify pending age returns below the SLO and that no money-moving event was
   lost or published twice.
