-- payout_requests.fee_amount (added nullable, services/payout/migrations/000004_quoted_fee.up.sql)
-- had no non-negativity CHECK, unlike its source fee_quotes.fee_amount
-- (CHECK (fee_amount >= 0 AND fee_amount < amount), services/ledger/migrations/000021_fee_quotes.up.sql)
-- and unlike services/payout/internal/payout/orchestrate.go's own use of FeeAmount.IsPositive()
-- as the fee-leg-inclusion gate. NULL stays valid (no fee quote was
-- consumed for this payout) — schema audit finding.
ALTER TABLE payout_requests ADD CONSTRAINT chk_payout_requests_fee_amount_nonnegative CHECK (fee_amount IS NULL OR fee_amount >= 0);
