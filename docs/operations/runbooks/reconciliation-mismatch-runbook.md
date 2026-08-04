# Reconciliation mismatch runbook

1. Stop or quarantine the affected settlement batch if the mismatch is
   material or unexplained.
2. Capture batch id, provider references, currencies, amounts, import hash,
   and the ledger statement snapshot.
3. Classify the mismatch as missing, duplicate, amount, currency, status, or
   timing. Check callback/outbox age before assuming loss.
4. Do not update a ledger transaction or balance directly. Create a typed
   correction request through the maker-checker path.
5. A checker independently reviews provider evidence and approves/rejects the
   correction. Re-run reconciliation after the append-only correction.
6. Escalate to SEV-1 if the team cannot establish the final monetary outcome.
