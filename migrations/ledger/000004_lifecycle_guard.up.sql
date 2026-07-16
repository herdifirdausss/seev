-- Lifecycle guard (docs/plan/14 Task T2, decision K3).
--
-- closed_by_tx_id/closed_reason make "close the original transaction"
-- (reverse, settle, cancel, release, refund) a single atomic, race-proof
-- operation: TransactionRepository.CloseOriginal does
--   UPDATE ledger_transactions SET closed_by_tx_id=$new, closed_reason=$r, ...
--   WHERE id=$original AND closed_by_tx_id IS NULL
-- and the caller checks RowsAffected. Two concurrent closers of the same
-- original (e.g. two reversal requests racing) can no longer both succeed —
-- exactly one UPDATE affects a row, the other affects zero and is treated as
-- apperror.ErrAlreadyClosed. This replaces the old check-then-update pattern
-- in Reversal (SELECT status, then a plain UPDATE with no WHERE guard),
-- which had a real TOCTOU window under READ COMMITTED.
ALTER TABLE ledger_transactions
    ADD COLUMN closed_by_tx_id UUID NULL UNIQUE REFERENCES ledger_transactions(id),
    ADD COLUMN closed_reason   TEXT NULL
        CHECK (closed_reason IN ('reversed','settled','cancelled','released','refunded')),
    ADD CONSTRAINT chk_closed_pair CHECK ((closed_by_tx_id IS NULL) = (closed_reason IS NULL));
