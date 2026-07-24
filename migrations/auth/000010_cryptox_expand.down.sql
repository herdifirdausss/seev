DROP INDEX IF EXISTS idx_auth_users_email_lookup_digest;
ALTER TABLE auth_users
    DROP COLUMN IF EXISTS email_ciphertext,
    DROP COLUMN IF EXISTS email_key_version,
    DROP COLUMN IF EXISTS email_lookup_digest,
    DROP COLUMN IF EXISTS full_name_ciphertext,
    DROP COLUMN IF EXISTS full_name_key_version;
ALTER TABLE kyc_submissions
    DROP COLUMN IF EXISTS payload_ciphertext,
    DROP COLUMN IF EXISTS payload_key_version,
    DROP COLUMN IF EXISTS rescreen_name,
    DROP COLUMN IF EXISTS rescreen_birth_date;
