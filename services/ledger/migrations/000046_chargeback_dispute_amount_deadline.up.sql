-- Enforce the cross-table dispute amount invariant at the database boundary,
-- not only in the Go service. The original transaction is immutable, so the
-- trigger is safe for both raw SQL and application writes.
CREATE OR REPLACE FUNCTION fn_validate_chargeback_dispute_amount() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    original_amount BIGINT;
    original_currency TEXT;
BEGIN
    SELECT amount, trim(currency) INTO original_amount, original_currency
    FROM ledger_transactions WHERE id = NEW.original_tx_id;
    IF original_amount IS NULL THEN
        RAISE EXCEPTION 'original ledger transaction does not exist';
    END IF;
    IF NEW.amount > original_amount THEN
        RAISE EXCEPTION 'chargeback amount cannot exceed original transaction amount';
    END IF;
    IF trim(NEW.currency) <> original_currency THEN
        RAISE EXCEPTION 'chargeback currency must match original transaction currency';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_validate_chargeback_dispute_amount
    BEFORE INSERT OR UPDATE OF original_tx_id, amount, currency ON chargeback_disputes
    FOR EACH ROW EXECUTE FUNCTION fn_validate_chargeback_dispute_amount();

CREATE INDEX idx_chargeback_disputes_due
    ON chargeback_disputes (evidence_due_at)
    WHERE status IN ('open', 'evidence_submitted') AND evidence_due_at IS NOT NULL;
