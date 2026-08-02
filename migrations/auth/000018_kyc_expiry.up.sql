-- Business-completeness audit finding: auth_users.kyc_level (000002_kyc)
-- never expires once approved — a user verified years ago with since-expired
-- identity documents keeps their tier's limits forever, with no
-- re-verification requirement. kyc_verified_until is the validity deadline
-- for the CURRENT kyc_level: set by ApproveKYCSubmission (internal/auth/
-- kyc.go's approveSubmission, using AuthConfig.KYCValidityTTL), cleared by
-- any downgrade (manual or the new expiry worker's own downgrade-to-0), and
-- NULL for level 0 (nothing to verify).
ALTER TABLE auth_users ADD COLUMN kyc_verified_until TIMESTAMPTZ NULL;

-- Grandfather every already-approved user with one fresh validity window
-- starting now, rather than backfilling NULL/expired — the latter would
-- make the expiry worker downgrade the entire existing L1/L2 user base the
-- moment this migration runs, which is an operational incident, not a
-- compliance improvement. 365 days here is a one-time migration-time
-- constant, deliberately independent of AuthConfig.KYCValidityTTL (a SQL
-- migration cannot read process env); ongoing approvals after this always
-- use the configured TTL.
UPDATE auth_users SET kyc_verified_until = now() + INTERVAL '365 days' WHERE kyc_level > 0;

-- Partial index for the periodic expiry scan: only rows that can possibly be
-- "expired and above L0" are indexed.
CREATE INDEX idx_auth_users_kyc_expiry
    ON auth_users (kyc_verified_until)
    WHERE kyc_level > 0 AND kyc_verified_until IS NOT NULL;
