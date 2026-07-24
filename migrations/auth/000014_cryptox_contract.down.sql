-- Structural rollback only — historical plaintext is not recoverable from
-- this migration once dropped (it never lived anywhere else). Re-adds
-- nullable plaintext columns so the pre-contract application code can run
-- again, but every row starts NULL; a genuine rollback requires restoring
-- from a pre-migration backup, not replaying this file.
ALTER TABLE kyc_submissions ADD COLUMN payload JSONB;
ALTER TABLE kyc_submissions
    ALTER COLUMN payload_ciphertext DROP NOT NULL,
    ALTER COLUMN payload_key_version DROP NOT NULL;

ALTER TABLE auth_users ADD COLUMN email TEXT;
ALTER TABLE auth_users ADD COLUMN full_name TEXT;
ALTER TABLE auth_users
    ALTER COLUMN email_ciphertext DROP NOT NULL,
    ALTER COLUMN email_key_version DROP NOT NULL,
    ALTER COLUMN email_lookup_digest DROP NOT NULL,
    ALTER COLUMN full_name_ciphertext DROP NOT NULL,
    ALTER COLUMN full_name_key_version DROP NOT NULL;

CREATE UNIQUE INDEX idx_auth_users_email ON auth_users (lower(email));
