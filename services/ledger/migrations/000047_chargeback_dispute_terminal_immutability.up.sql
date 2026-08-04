-- Terminal dispute outcomes are append-only. The chargeback transaction link
-- remains separately mutable because the forced-debit posting can commit
-- before or after the network decision; all case identity, evidence, status,
-- and resolution fields become immutable once a terminal outcome is recorded.
CREATE OR REPLACE FUNCTION fn_chargeback_dispute_terminal_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('won', 'lost', 'expired') AND (
        NEW.original_tx_id IS DISTINCT FROM OLD.original_tx_id OR
        NEW.dispute_ref IS DISTINCT FROM OLD.dispute_ref OR
        NEW.card_network IS DISTINCT FROM OLD.card_network OR
        NEW.reason_code IS DISTINCT FROM OLD.reason_code OR
        NEW.amount IS DISTINCT FROM OLD.amount OR
        NEW.currency IS DISTINCT FROM OLD.currency OR
        NEW.status IS DISTINCT FROM OLD.status OR
        NEW.evidence_due_at IS DISTINCT FROM OLD.evidence_due_at OR
        NEW.evidence_ref IS DISTINCT FROM OLD.evidence_ref OR
        NEW.resolved_at IS DISTINCT FROM OLD.resolved_at OR
        NEW.resolved_by IS DISTINCT FROM OLD.resolved_by OR
        NEW.resolution_reason IS DISTINCT FROM OLD.resolution_reason OR
        NEW.created_by IS DISTINCT FROM OLD.created_by OR
        NEW.created_at IS DISTINCT FROM OLD.created_at
    ) THEN
        RAISE EXCEPTION 'terminal chargeback dispute fields are immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_chargeback_dispute_terminal_immutable
    BEFORE UPDATE ON chargeback_disputes
    FOR EACH ROW EXECUTE FUNCTION fn_chargeback_dispute_terminal_immutable();
