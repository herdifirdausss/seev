-- Plan 46 T2: extend the T1 retry intent to cover limits-first downgrades.
ALTER TABLE kyc_apply_retries
    ALTER COLUMN submission_id DROP NOT NULL;

ALTER TABLE kyc_apply_retries
    DROP CONSTRAINT IF EXISTS kyc_apply_retries_level_check,
    ADD CONSTRAINT kyc_apply_retries_level_check CHECK (level IN (0, 1, 2)),
    ADD COLUMN direction TEXT NOT NULL DEFAULT 'upgrade'
        CHECK (direction IN ('upgrade', 'downgrade')),
    ADD COLUMN decided_by TEXT,
    ADD COLUMN decision_reason TEXT;
