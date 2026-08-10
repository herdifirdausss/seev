-- Validate existing rows explicitly before activating the invariant. Level 0
-- has no active verification window; every verified level must have the
-- deadline populated by the approval workflow.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM auth_users
        WHERE (kyc_level = 0) <> (kyc_verified_until IS NULL)
    ) THEN
        RAISE EXCEPTION 'auth_users contains inconsistent KYC level/validity rows';
    END IF;
END;
$$;

ALTER TABLE auth_users
    ADD CONSTRAINT chk_auth_users_kyc_validity_consistency
    CHECK ((kyc_level = 0) = (kyc_verified_until IS NULL)) NOT VALID;
ALTER TABLE auth_users VALIDATE CONSTRAINT chk_auth_users_kyc_validity_consistency;
