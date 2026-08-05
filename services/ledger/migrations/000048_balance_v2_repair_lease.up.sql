-- C6: lease-based recovery for a data_migration_repairs row stuck in
-- 'running' after a repair-worker crash. Spec §18.5 calls for lease_owner/
-- lease_expires_at on this table; 000041 omitted them, leaving no recovery
-- path distinct from data_migration_checkpoints' existing lease pattern.

ALTER TABLE data_migration_repairs
    ADD COLUMN lease_owner      TEXT NULL,
    ADD COLUMN lease_expires_at TIMESTAMPTZ NULL;

CREATE INDEX idx_data_migration_repairs_lease
    ON data_migration_repairs(lease_expires_at);
