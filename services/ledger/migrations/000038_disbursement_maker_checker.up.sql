-- Business-completeness audit finding: bulk disbursement could move
-- platform funds to hundreds of users with ONE operator doing both Import
-- and Run — no second-approval gate, unlike manual adjustments
-- (pending_adjustments, services/ledger/migrations/000005) which already require a
-- DIFFERENT identity to approve before any money moves. A batch's
-- aggregate value routinely exceeds any single manual adjustment, so this
-- closes that gap with the same maker-checker shape: Import creates a
-- batch in 'pending_approval' (posts nothing), a second identity must
-- Approve it before Run will process a single item.
ALTER TABLE disbursement_batches
    DROP CONSTRAINT disbursement_batches_status_check,
    ALTER COLUMN status SET DEFAULT 'pending_approval',
    ADD CONSTRAINT disbursement_batches_status_check
        CHECK (status IN ('pending_approval', 'rejected', 'processing', 'completed', 'completed_with_errors')),
    ADD COLUMN approved_by TEXT NULL,
    ADD COLUMN approved_at TIMESTAMPTZ NULL,
    ADD COLUMN decision_reason TEXT NULL,
    ADD CONSTRAINT chk_disbursement_batches_approver_not_creator CHECK (approved_by IS NULL OR approved_by <> created_by);
