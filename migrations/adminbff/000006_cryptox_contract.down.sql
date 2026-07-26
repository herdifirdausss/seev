-- Structural rollback only. Historical plaintext is intentionally not
-- recreated from ciphertext; use a pre-contract restore when required.
ALTER TABLE sessions ADD COLUMN email TEXT NOT NULL DEFAULT 'REDACTED';
