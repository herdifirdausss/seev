ALTER TABLE auth_users
    DROP CONSTRAINT IF EXISTS chk_auth_users_kyc_validity_consistency;
