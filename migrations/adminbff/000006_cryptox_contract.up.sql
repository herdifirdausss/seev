-- A8 T2 contract phase: sessions.email has been backfilled during the
-- expand phase and must no longer be available as a plaintext fallback.
ALTER TABLE sessions
    ALTER COLUMN email_ciphertext SET NOT NULL,
    ALTER COLUMN email_key_version SET NOT NULL;

ALTER TABLE sessions DROP COLUMN email;
