DROP INDEX IF EXISTS idx_auth_users_kyc_expiry;
ALTER TABLE auth_users DROP COLUMN IF EXISTS kyc_verified_until;
