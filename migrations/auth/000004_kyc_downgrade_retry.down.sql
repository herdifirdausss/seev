ALTER TABLE kyc_apply_retries
    DROP COLUMN IF EXISTS decision_reason,
    DROP COLUMN IF EXISTS decided_by,
    DROP COLUMN IF EXISTS direction;

ALTER TABLE kyc_apply_retries
    DROP CONSTRAINT IF EXISTS kyc_apply_retries_level_check,
    ADD CONSTRAINT kyc_apply_retries_level_check CHECK (level IN (1, 2));

ALTER TABLE kyc_apply_retries
    ALTER COLUMN submission_id SET NOT NULL;
