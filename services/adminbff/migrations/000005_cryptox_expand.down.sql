ALTER TABLE sessions
    DROP COLUMN IF EXISTS email_ciphertext,
    DROP COLUMN IF EXISTS email_key_version;
