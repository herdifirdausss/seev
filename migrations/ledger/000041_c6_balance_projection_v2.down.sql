DROP POLICY IF EXISTS pol_read_readonly ON account_balances_v2;
DROP POLICY IF EXISTS pol_all_service ON account_balances_v2;
DROP POLICY IF EXISTS pol_all_service ON data_migrations;
DROP POLICY IF EXISTS pol_all_service ON data_migration_checkpoints;
DROP POLICY IF EXISTS pol_all_service ON data_migration_runs;
DROP POLICY IF EXISTS pol_all_service ON data_migration_mismatches;
DROP POLICY IF EXISTS pol_all_service ON data_migration_repairs;
DROP POLICY IF EXISTS pol_all_service ON data_migration_transitions;

REVOKE ALL PRIVILEGES ON
    account_balances_v2, data_migrations, data_migration_checkpoints,
    data_migration_runs, data_migration_mismatches, data_migration_repairs,
    data_migration_transitions
FROM app_service, app_readonly;

DROP TABLE IF EXISTS data_migration_transitions;
DROP INDEX IF EXISTS uq_data_migration_repairs_idempotent;
DROP TABLE IF EXISTS data_migration_repairs;
DROP TABLE IF EXISTS data_migration_mismatches;
DROP TABLE IF EXISTS data_migration_runs;
DROP TABLE IF EXISTS data_migration_checkpoints;
DROP INDEX IF EXISTS uq_data_migrations_active_resource;
DROP TABLE IF EXISTS data_migrations;
DROP TRIGGER IF EXISTS trg_account_balances_v2_ua ON account_balances_v2;
DROP TABLE IF EXISTS account_balances_v2;

DROP TRIGGER IF EXISTS trg_account_balances_version ON account_balances;
DROP FUNCTION IF EXISTS fn_account_balance_version();
ALTER TABLE account_balances
    DROP CONSTRAINT IF EXISTS chk_account_balances_version_nonnegative,
    DROP COLUMN IF EXISTS version;
