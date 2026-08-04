ALTER TABLE disbursement_batches
    DROP CONSTRAINT IF EXISTS chk_disbursement_batches_approver_not_creator,
    DROP COLUMN IF EXISTS decision_reason,
    DROP COLUMN IF EXISTS approved_at,
    DROP COLUMN IF EXISTS approved_by,
    DROP CONSTRAINT disbursement_batches_status_check,
    ALTER COLUMN status SET DEFAULT 'processing',
    ADD CONSTRAINT disbursement_batches_status_check
        CHECK (status IN ('processing', 'completed', 'completed_with_errors'));
