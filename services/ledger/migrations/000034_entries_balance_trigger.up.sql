-- Business-completeness audit finding: double-entry balance was enforced
-- only by application code plus fn_verify_ledger_balance's periodic
-- (daily-job) detective check — a bug in a processor's BuildEntries()
-- could write an unbalanced transaction and it would only surface later,
-- after the fact. This adds a DB-level preventive guarantee: no unbalanced
-- transaction can ever commit.
--
-- DEFERRABLE INITIALLY DEFERRED, checked at COMMIT (not per-statement):
-- production's own InsertEntries always inserts a whole transaction's
-- entries in one multi-row statement
-- (services/ledger/internal/repository/ledger_entry_repository.go), but at least one
-- existing integration test fixture (operations/recovery/drreseed/runner_integration_test.go's
-- seedPostedTransfer) legitimately inserts the debit and credit legs via
-- two SEPARATE statements within the same DB transaction — discovered live
-- when a first attempt at a FOR EACH STATEMENT trigger rejected that
-- fixture's debit-only first statement even though the transaction was
-- balanced by the time it committed. Deferring to commit time handles both
-- shapes correctly and is robust to any future insertion pattern, not just
-- the one shape production code happens to use today. Postgres only
-- supports FOR EACH ROW for a real CONSTRAINT TRIGGER (no per-statement
-- deferred form), so this re-aggregates from the table itself per row
-- rather than reading a NEW TABLE transition relation — idx_entries_tx
-- (migrations/000001_ledger_core.up.sql) keeps that cheap, and the common
-- case (production's 2-row batch) only pays for it twice.
--
-- Currency is not considered here, matching fn_verify_ledger_balance's own
-- existing logic exactly: every processor keeps one ledger transaction to
-- one currency by design (see services/ledger/internal/processors/fx_in.go's own
-- doc comment — "one transaction = one currency; cross-currency 'balance'
-- is an FX position tracked outside the ledger, not enforced by it" — FX
-- conversion is two SEPARATE single-currency transactions, each
-- individually balanced, not one transaction spanning two currencies).
CREATE OR REPLACE FUNCTION fn_ledger_entries_check_balanced() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_debit  BIGINT;
    v_credit BIGINT;
BEGIN
    SELECT COALESCE(SUM(amount) FILTER (WHERE direction = 'debit'),  0),
           COALESCE(SUM(amount) FILTER (WHERE direction = 'credit'), 0)
    INTO v_debit, v_credit
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id;

    IF v_debit IS DISTINCT FROM v_credit THEN
        RAISE EXCEPTION 'ledger_entries: transaction % is not balanced (sum(debit)=% sum(credit)=%)',
            NEW.transaction_id, v_debit, v_credit
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER trg_ledger_entries_balanced
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION fn_ledger_entries_check_balanced();
