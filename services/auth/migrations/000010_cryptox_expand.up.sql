-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.3 (K2/K3 expand phase): nullable
-- ciphertext/key-version/lookup-digest columns for auth_users.email,
-- auth_users.full_name, and kyc_submissions.payload. Plaintext columns
-- stay in place and required — application code dual-writes both during
-- this phase (K3 step 2); the contract phase (making ciphertext required,
-- dropping plaintext) is T2.5, gated on a real backfill + verification
-- pass, never bundled into the same migration as expand.
ALTER TABLE auth_users
    ADD COLUMN email_ciphertext     BYTEA,
    ADD COLUMN email_key_version    INT,
    ADD COLUMN email_lookup_digest  BYTEA,
    ADD COLUMN full_name_ciphertext  BYTEA,
    ADD COLUMN full_name_key_version INT;

-- Partial (WHERE ... IS NOT NULL) so pre-migration rows with no digest yet
-- never collide with each other on NULL — only real digest values are
-- constrained unique, matching lower(email)'s own existing uniqueness
-- intent but over the encrypted representation.
CREATE UNIQUE INDEX idx_auth_users_email_lookup_digest
    ON auth_users (email_lookup_digest)
    WHERE email_lookup_digest IS NOT NULL;

ALTER TABLE kyc_submissions
    ADD COLUMN payload_ciphertext  BYTEA,
    ADD COLUMN payload_key_version INT;

-- ListKYCRescreenSubjects (kyc_repository.go) reads name/birth_date out of
-- the plaintext payload JSONB via a SQL-level DISTINCT ON/ORDER BY/LIMIT
-- query fraud-service's sanctions rescreen job depends on — that query
-- cannot run against ciphertext without decrypting every row in
-- application code, defeating its own indexed, paginated design. These
-- two fields are already the ONLY ones ListKYCRescreenSubjects's own
-- comment says leave auth-service ("only the fields required by the
-- sanctions rule are selected; the original KYC payload remains inside
-- auth-service") — i.e. already treated as less sensitive than the rest
-- of payload, not newly exposed by staying queryable here. Populated by
-- application code at the same time payload_ciphertext is written; the
-- full payload itself is still fully encrypted.
ALTER TABLE kyc_submissions
    ADD COLUMN rescreen_name       TEXT,
    ADD COLUMN rescreen_birth_date TEXT;

-- No grant changes: app_service already has table-level UPDATE/INSERT on
-- both tables (migrations 000001/000002/000003) — Postgres column-level
-- privileges are only restricted when explicitly granted per-column,
-- which these never were, so newly added columns are already covered.
