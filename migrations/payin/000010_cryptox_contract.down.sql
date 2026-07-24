-- Structural rollback only — historical plaintext for already-redacted
-- rows (raw_ciphertext already NULL) is not recoverable; a genuine
-- rollback requires restoring from a pre-migration backup.
ALTER TABLE payin_webhook_events ADD COLUMN raw JSONB NOT NULL DEFAULT '{}'::jsonb;
