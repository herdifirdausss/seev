-- Auth owns verified contact state. Gateway may resolve this projection but
-- never reads auth_users directly. Existing accounts are grandfathered as
-- verified because the pre-C3 application had no email-verification flow;
-- new accounts remain unverified until an Auth-owned verification workflow
-- sets this timestamp explicitly.
ALTER TABLE auth_users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;
UPDATE auth_users SET email_verified_at = COALESCE(created_at, now()) WHERE email_verified_at IS NULL;
