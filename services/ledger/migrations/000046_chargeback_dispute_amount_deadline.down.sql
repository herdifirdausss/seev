DROP TRIGGER IF EXISTS trg_validate_chargeback_dispute_amount ON chargeback_disputes;
DROP FUNCTION IF EXISTS fn_validate_chargeback_dispute_amount();
DROP INDEX IF EXISTS idx_chargeback_disputes_due;
