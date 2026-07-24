ALTER TABLE payin_webhook_events
    DROP COLUMN IF EXISTS raw_ciphertext,
    DROP COLUMN IF EXISTS raw_key_version;
