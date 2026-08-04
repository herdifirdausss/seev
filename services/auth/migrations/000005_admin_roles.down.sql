UPDATE auth_users SET role = 'admin' WHERE role IN ('admin_maker', 'admin_checker');
ALTER TABLE auth_users DROP CONSTRAINT IF EXISTS auth_users_role_check;
ALTER TABLE auth_users ADD CONSTRAINT auth_users_role_check
    CHECK (role IN ('user', 'admin'));
