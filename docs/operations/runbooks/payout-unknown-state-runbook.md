# Payout unknown-state runbook

Use this when a provider request times out or returns an ambiguous result.

1. Declare SEV-1 if the intent may have settled and the provider outcome is
   outside its stated SLA. Record the intent id, provider reference, and
   correlation ID.
2. Stop blind retries. Confirm the payout remains in `unknown`/recovery state
   and that no new vendor request was created.
3. Query the same provider using the persisted provider request/reference. Do
   not query by a newly generated id.
4. If the provider says settled, close the original intent through the normal
   idempotent settlement path. If failed, release the reservation through its
   compensating path. If still unknown, keep it pinned and escalate.
5. Run ledger balance and duplicate-settlement checks. Preserve the provider
   response and event hashes in the incident artifact.
6. Replay only the original durable recovery command after the dependency is
   healthy. Never edit a posted transaction.
