-- payin_webhook_events.amount was the one amount column in the schema left
-- unconstrained (schema audit finding) — every comparable column
-- (ledger_transactions.amount, payin_topup_intents.amount,
-- payout_requests.amount, fee_quotes.amount, recon_items.amount,
-- screening_events.amount) already has CHECK (amount > 0). Application
-- code already enforces this (internal/payin/payin.go's HandleVendorCallback
-- checks amount.IsPositive() before insert), so this is a DB-level backstop
-- matching an invariant the app already guarantees — same reasoning as
-- migrations/ledger/000033_recon_items_amount_check.up.sql.
ALTER TABLE payin_webhook_events ADD CONSTRAINT chk_payin_webhook_events_amount_positive CHECK (amount > 0);
