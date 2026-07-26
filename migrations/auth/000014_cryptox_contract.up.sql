-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.5's own follow-up
-- "A8 T2.5b" (contract migration): the expand-phase backfill
-- (migrations/auth/000010_cryptox_expand.up.sql) has had its bake period —
-- every auth_users/kyc_submissions row now carries ciphertext. This
-- migration is the CONTRACT phase: plaintext columns are dropped, the
-- ciphertext/key-version/digest columns become the only copy (NOT NULL),
-- and the now-redundant plaintext-based unique index is replaced.
--
-- idx_auth_users_email indexed lower(email) — now superseded entirely by
-- idx_auth_users_email_lookup_digest (a partial unique index on the
-- deterministic digest, already in place since 000010).
DROP INDEX idx_auth_users_email;

ALTER TABLE auth_users
    ALTER COLUMN email_ciphertext SET NOT NULL,
    ALTER COLUMN email_key_version SET NOT NULL,
    ALTER COLUMN email_lookup_digest SET NOT NULL,
    ALTER COLUMN full_name_ciphertext SET NOT NULL,
    ALTER COLUMN full_name_key_version SET NOT NULL;

ALTER TABLE auth_users
    DROP COLUMN email,
    DROP COLUMN full_name;

ALTER TABLE kyc_submissions
    ALTER COLUMN payload_ciphertext SET NOT NULL,
    ALTER COLUMN payload_key_version SET NOT NULL;

ALTER TABLE kyc_submissions
    DROP COLUMN payload;
