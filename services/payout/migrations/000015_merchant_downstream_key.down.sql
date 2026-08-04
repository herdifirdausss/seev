DROP INDEX IF EXISTS idx_payout_requests_merchant_downstream_key;
ALTER TABLE payout_requests DROP COLUMN downstream_key;
