DROP INDEX IF EXISTS idx_data_migration_repairs_lease;
ALTER TABLE data_migration_repairs
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS lease_owner;
