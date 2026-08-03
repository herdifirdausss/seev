-- C5 top-up fee snapshot.  Existing intents are explicitly fee-free and keep
-- their old amount meaning: amount is the wallet credit amount.
ALTER TABLE payin_topup_intents
    ADD COLUMN IF NOT EXISTS fee_quote_id UUID NULL,
    ADD COLUMN IF NOT EXISTS fee_rule_id UUID NULL,
    ADD COLUMN IF NOT EXISTS fee_gateway TEXT NULL,
    ADD COLUMN IF NOT EXISTS gateway TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS fee_amount BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_debit BIGINT,
    ADD COLUMN IF NOT EXISTS fee_application TEXT NOT NULL DEFAULT 'added_on_top',
    ADD COLUMN IF NOT EXISTS fee_quote_consumed_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS fee_snapshot_version INTEGER NOT NULL DEFAULT 1;

-- Persist the same immutable financial snapshot on the normalized webhook
-- evidence row. This keeps an operator/replay record self-describing even
-- after an intent is retained separately or a provider sends a duplicate.
ALTER TABLE payin_webhook_events
    ADD COLUMN IF NOT EXISTS fee_quote_id UUID NULL,
    ADD COLUMN IF NOT EXISTS fee_rule_id UUID NULL,
    ADD COLUMN IF NOT EXISTS fee_gateway TEXT NULL,
    ADD COLUMN IF NOT EXISTS fee_amount BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_debit BIGINT,
    ADD COLUMN IF NOT EXISTS fee_application TEXT NOT NULL DEFAULT 'added_on_top',
    ADD COLUMN IF NOT EXISTS fee_quote_consumed_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS fee_snapshot_version INTEGER NOT NULL DEFAULT 1;

UPDATE payin_webhook_events
SET fee_amount = COALESCE(fee_amount, 0),
    total_debit = COALESCE(total_debit, amount),
    fee_application = COALESCE(fee_application, 'added_on_top'),
    fee_snapshot_version = COALESCE(fee_snapshot_version, 1);

UPDATE payin_topup_intents
SET fee_amount = COALESCE(fee_amount, 0),
    total_debit = COALESCE(total_debit, amount),
    fee_application = COALESCE(fee_application, 'added_on_top'),
    fee_snapshot_version = COALESCE(fee_snapshot_version, 1);

ALTER TABLE payin_topup_intents
    ALTER COLUMN total_debit SET DEFAULT 0,
    ALTER COLUMN total_debit SET NOT NULL;

ALTER TABLE payin_topup_intents
    ADD CONSTRAINT payin_topup_fee_amount_non_negative CHECK (fee_amount >= 0),
    ADD CONSTRAINT payin_topup_total_debit_invariant CHECK (total_debit >= amount AND total_debit = amount + fee_amount),
    ADD CONSTRAINT payin_topup_fee_application_check CHECK (fee_application = 'added_on_top'),
    ADD CONSTRAINT payin_topup_fee_snapshot_version_check CHECK (fee_snapshot_version > 0);

ALTER TABLE payin_webhook_events
    ALTER COLUMN total_debit SET DEFAULT 0,
    ALTER COLUMN total_debit SET NOT NULL;

ALTER TABLE payin_webhook_events
    ADD CONSTRAINT payin_webhook_fee_amount_non_negative CHECK (fee_amount >= 0),
    ADD CONSTRAINT payin_webhook_total_debit_invariant CHECK (total_debit = amount AND total_debit >= fee_amount),
    ADD CONSTRAINT payin_webhook_fee_application_check CHECK (fee_application = 'added_on_top'),
    ADD CONSTRAINT payin_webhook_fee_snapshot_version_check CHECK (fee_snapshot_version > 0);

CREATE INDEX idx_payin_topup_intents_fee_quote ON payin_topup_intents(fee_quote_id)
    WHERE fee_quote_id IS NOT NULL;
CREATE UNIQUE INDEX uq_payin_topup_intents_fee_quote ON payin_topup_intents(fee_quote_id)
    WHERE fee_quote_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE ON payin_topup_intents TO app_service;
