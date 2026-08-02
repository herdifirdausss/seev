DROP TABLE IF EXISTS chargeback_dispute_status_changes;

ALTER TABLE chargeback_disputes DROP CONSTRAINT IF EXISTS chk_chargeback_disputes_resolved_fields_together;
ALTER TABLE chargeback_disputes DROP COLUMN IF EXISTS resolved_by;
ALTER TABLE chargeback_disputes
    ADD CONSTRAINT chk_chargeback_disputes_resolved_fields_together CHECK (
        (status IN ('won', 'lost', 'expired')) = (resolved_at IS NOT NULL)
    );
