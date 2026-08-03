-- C6: zero-downtime Ledger balance projection migration.
-- The existing account_balances table remains authoritative throughout the
-- migration. The v2 table is additive and is never written by a trigger.

ALTER TABLE account_balances
    ADD COLUMN version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT chk_account_balances_version_nonnegative CHECK (version >= 0);

CREATE OR REPLACE FUNCTION fn_account_balance_version() RETURNS TRIGGER AS $$
BEGIN
    NEW.version = OLD.version + 1;
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_account_balances_version
    BEFORE UPDATE OF balance, allow_negative ON account_balances
    FOR EACH ROW EXECUTE FUNCTION fn_account_balance_version();

CREATE TABLE account_balances_v2 (
    account_id          UUID PRIMARY KEY REFERENCES accounts(id),
    account_type        TEXT NOT NULL CHECK (account_type IN
                            ('cash','hold','pending','frozen','pocket','fee',
                             'settlement','escrow','chargeback','confiscated',
                             'adjustment','suspense','fx_conversion','interest_expense',
                             'accrued_interest_payable')),
    currency            CHAR(3) NOT NULL,
    allow_negative      BOOLEAN NOT NULL DEFAULT false,
    available_amount    BIGINT NOT NULL DEFAULT 0,
    reserved_amount     BIGINT NOT NULL DEFAULT 0,
    pending_amount      BIGINT NOT NULL DEFAULT 0,
    restricted_amount   BIGINT NOT NULL DEFAULT 0,
    source_version      BIGINT NOT NULL CHECK (source_version >= 0),
    last_transaction_id UUID NULL REFERENCES ledger_transactions(id),
    projection_checksum BYTEA NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_account_balances_v2_source_version
    ON account_balances_v2(source_version, account_id);
CREATE INDEX idx_account_balances_v2_updated_at
    ON account_balances_v2(updated_at DESC);

CREATE TRIGGER trg_account_balances_v2_ua
    BEFORE UPDATE ON account_balances_v2
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TABLE data_migrations (
    id                          UUID PRIMARY KEY,
    public_id                   TEXT NOT NULL UNIQUE,
    name                        TEXT NOT NULL UNIQUE,
    resource                    TEXT NOT NULL,
    source_version              TEXT NOT NULL,
    target_version              TEXT NOT NULL,
    state                       TEXT NOT NULL CHECK (state IN
        ('draft','validated','target_ready','backfilling','dual_write_shadow',
         'shadow_read','canary_read','ramping_read','target_primary',
         'source_write_disabled','observation','completed','paused',
         'rolling_back','rolled_back','failed','cancelled_before_write')),
    previous_state              TEXT NULL,
    read_percentage_basis_points INT NOT NULL DEFAULT 0 CHECK (read_percentage_basis_points BETWEEN 0 AND 10000),
    shadow_percentage_basis_points INT NOT NULL DEFAULT 0 CHECK (shadow_percentage_basis_points BETWEEN 0 AND 10000),
    strict_dual_write           BOOLEAN NOT NULL DEFAULT false,
    source_fallback_enabled     BOOLEAN NOT NULL DEFAULT true,
    source_write_enabled        BOOLEAN NOT NULL DEFAULT true,
    target_write_enabled        BOOLEAN NOT NULL DEFAULT false,
    transform_version           INT NOT NULL,
    baseline_commit             TEXT NOT NULL,
    created_by                  TEXT NOT NULL,
    updated_by                  TEXT NOT NULL,
    pause_reason                TEXT NULL,
    failure_code                TEXT NULL,
    started_at                  TIMESTAMPTZ NULL,
    backfill_completed_at       TIMESTAMPTZ NULL,
    cutover_started_at          TIMESTAMPTZ NULL,
    completed_at                TIMESTAMPTZ NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    version                     BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE INDEX idx_data_migrations_state ON data_migrations(state, updated_at DESC);
CREATE UNIQUE INDEX uq_data_migrations_active_resource
    ON data_migrations(resource)
    WHERE state NOT IN ('completed', 'rolled_back', 'cancelled_before_write');

CREATE TABLE data_migration_checkpoints (
    id                UUID PRIMARY KEY,
    migration_id      UUID NOT NULL REFERENCES data_migrations(id),
    worker_kind       TEXT NOT NULL,
    partition_key     TEXT NOT NULL,
    last_source_key   TEXT NULL,
    watermark_version BIGINT NULL,
    processed_count   BIGINT NOT NULL DEFAULT 0 CHECK (processed_count >= 0),
    updated_count     BIGINT NOT NULL DEFAULT 0 CHECK (updated_count >= 0),
    skipped_count     BIGINT NOT NULL DEFAULT 0 CHECK (skipped_count >= 0),
    failed_count      BIGINT NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    lease_owner       TEXT NULL,
    lease_expires_at  TIMESTAMPTZ NULL,
    status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','paused')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (migration_id, worker_kind, partition_key)
);

CREATE INDEX idx_data_migration_checkpoints_lease
    ON data_migration_checkpoints(lease_expires_at);

CREATE TABLE data_migration_runs (
    id               UUID PRIMARY KEY,
    migration_id     UUID NOT NULL REFERENCES data_migrations(id),
    run_type         TEXT NOT NULL CHECK (run_type IN ('sample','bucket','full','incident','pre_cutover','post_cutover')),
    status           TEXT NOT NULL CHECK (status IN ('running','completed','failed','cancelled')),
    started_at       TIMESTAMPTZ NOT NULL,
    finished_at      TIMESTAMPTZ NULL,
    source_cutoff    TEXT NULL,
    target_cutoff    TEXT NULL,
    processed_count  BIGINT NOT NULL DEFAULT 0,
    match_count      BIGINT NOT NULL DEFAULT 0,
    mismatch_count   BIGINT NOT NULL DEFAULT 0,
    error_count      BIGINT NOT NULL DEFAULT 0,
    evidence         JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_data_migration_runs_latest
    ON data_migration_runs(migration_id, finished_at DESC NULLS LAST);

CREATE TABLE data_migration_mismatches (
    id                    UUID PRIMARY KEY,
    migration_id          UUID NOT NULL REFERENCES data_migrations(id),
    resource_key_hash     BYTEA NOT NULL,
    resource_public_key   TEXT NOT NULL,
    classification        TEXT NOT NULL,
    status                TEXT NOT NULL CHECK (status IN
        ('open','classified','repair_pending','repairing','repaired','verified','ignored_with_reason','blocked')),
    severity              TEXT NOT NULL CHECK (severity IN ('warning','critical')),
    field_mask            BIGINT NOT NULL DEFAULT 0,
    source_version        BIGINT NULL,
    target_version        BIGINT NULL,
    source_checksum       BYTEA NULL,
    target_checksum       BYTEA NULL,
    occurrence_count      BIGINT NOT NULL DEFAULT 1,
    first_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    repair_attempt_count  INT NOT NULL DEFAULT 0,
    last_error_code       TEXT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (migration_id, resource_key_hash)
);

CREATE INDEX idx_data_migration_mismatches_gate
    ON data_migration_mismatches(migration_id, severity, status);

CREATE TABLE data_migration_repairs (
    id                    UUID PRIMARY KEY,
    migration_id          UUID NOT NULL REFERENCES data_migrations(id),
    mismatch_id           UUID NOT NULL REFERENCES data_migration_mismatches(id),
    resource_key_hash     BYTEA NOT NULL,
    repair_type           TEXT NOT NULL CHECK (repair_type IN ('target_rebuild','target_delete_recreate')),
    expected_source_version BIGINT NULL,
    status                TEXT NOT NULL CHECK (status IN ('pending_approval','approved','running','completed','failed','rejected')),
    attempt_count         INT NOT NULL DEFAULT 0,
    created_by            TEXT NOT NULL,
    approved_by           TEXT NULL,
    reason                TEXT NOT NULL,
    started_at            TIMESTAMPTZ NULL,
    finished_at           TIMESTAMPTZ NULL,
    error_code            TEXT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_migration_repair_checker CHECK (approved_by IS NULL OR approved_by <> created_by)
);

CREATE INDEX idx_data_migration_repairs_status
    ON data_migration_repairs(migration_id, status, created_at);
CREATE UNIQUE INDEX uq_data_migration_repairs_idempotent
    ON data_migration_repairs(
        migration_id, mismatch_id, repair_type,
        COALESCE(expected_source_version, -1)
    );

CREATE TABLE data_migration_transitions (
    id                 UUID PRIMARY KEY,
    migration_id       UUID NOT NULL REFERENCES data_migrations(id),
    from_state         TEXT NOT NULL,
    to_state           TEXT NOT NULL,
    requested_by       TEXT NOT NULL,
    approved_by        TEXT NULL,
    reason             TEXT NOT NULL,
    evidence_snapshot  JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_data_migration_transitions_migration
    ON data_migration_transitions(migration_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON
    account_balances_v2, data_migrations, data_migration_checkpoints,
    data_migration_runs, data_migration_mismatches, data_migration_repairs,
    data_migration_transitions
TO app_service;

GRANT SELECT ON account_balances_v2 TO app_readonly;

ALTER TABLE account_balances_v2 ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_balances_v2 FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON account_balances_v2
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON account_balances_v2
    FOR SELECT TO app_readonly USING (true);

ALTER TABLE data_migrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_migrations FORCE ROW LEVEL SECURITY;
ALTER TABLE data_migration_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_migration_checkpoints FORCE ROW LEVEL SECURITY;
ALTER TABLE data_migration_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_migration_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE data_migration_mismatches ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_migration_mismatches FORCE ROW LEVEL SECURITY;
ALTER TABLE data_migration_repairs ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_migration_repairs FORCE ROW LEVEL SECURITY;
ALTER TABLE data_migration_transitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_migration_transitions FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON data_migrations
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON data_migration_checkpoints
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON data_migration_runs
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON data_migration_mismatches
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON data_migration_repairs
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON data_migration_transitions
    FOR ALL TO app_service USING (true) WITH CHECK (true);
