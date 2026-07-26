-- Plan 51 K10: the account-closure finalizer removes the subject's login
-- credential in the same transaction that tombstones auth_users. The service
-- role already owns SELECT/INSERT/UPDATE on this table, but the original grant
-- intentionally omitted DELETE before a legitimate runtime delete path
-- existed.
GRANT DELETE ON auth_credentials TO app_service;
