-- chargeback_disputes.chargeback_tx_id (migrations/ledger/000035) had no
-- supporting index — only idx_chargeback_disputes_original_tx covers
-- original_tx_id. The migration's own doc comment describes exactly the
-- ops query this backs: "which dispute did this chargeback transaction
-- settle" (schema audit finding).
CREATE INDEX idx_chargeback_disputes_chargeback_tx ON chargeback_disputes(chargeback_tx_id) WHERE chargeback_tx_id IS NOT NULL;
