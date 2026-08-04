ALTER TABLE ledger_transactions ALTER COLUMN idempotency_key SET NOT NULL;
DROP INDEX IF EXISTS uq_ltx_idempotency_digest;
ALTER TABLE ledger_transactions
    DROP COLUMN IF EXISTS idempotency_key_digest,
    DROP COLUMN IF EXISTS idempotency_key_version,
    DROP COLUMN IF EXISTS conflict_fingerprint;
