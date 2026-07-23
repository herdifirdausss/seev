ALTER TABLE auth_users DROP CONSTRAINT IF EXISTS auth_users_role_check;
ALTER TABLE auth_users ADD CONSTRAINT auth_users_role_check
    CHECK (role IN ('user', 'admin', 'admin_maker', 'admin_checker'));
