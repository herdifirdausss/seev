-- Structural rollback only — historical plaintext for already-redacted
-- rows (destination_ciphertext already NULL) is not recoverable; a
-- genuine rollback requires restoring from a pre-migration backup.
ALTER TABLE payout_requests ADD COLUMN destination JSONB NOT NULL DEFAULT '{}'::jsonb;
