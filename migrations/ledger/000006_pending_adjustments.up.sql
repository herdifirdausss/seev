-- Maker-checker for manual balance adjustments (docs/roadmap/archive/16 Task T1,
-- decision K8). No single identity can move money via adjustment_credit/
-- adjustment_debit alone — see the revoked direct access in transport.
--
-- approved_by is used for BOTH decisions (approve AND reject) — whoever
-- decided, recorded either way, and in both cases must differ from
-- requested_by. The constraint below is the enforcement of last resort: it
-- holds even if application code has a bug or someone bypasses Go entirely
-- with a raw UPDATE.
CREATE TABLE pending_adjustments (
    id             UUID        PRIMARY KEY,
    requested_by   TEXT        NOT NULL,
    approved_by    TEXT        NULL,
    cmd_payload    JSONB       NOT NULL,
    reason         TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','approved','rejected','executed','failed')),
    executed_tx_id UUID        NULL REFERENCES ledger_transactions(id),
    error_message  TEXT        NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at     TIMESTAMPTZ NULL,

    CHECK (approved_by IS NULL OR approved_by <> requested_by)
);

CREATE INDEX idx_pending_adjustments_status ON pending_adjustments(status, created_at);
