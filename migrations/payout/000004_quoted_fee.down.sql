ALTER TABLE payout_requests DROP CONSTRAINT payout_requests_status_check;
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_status_check
    CHECK (status IN ('created','held','submitted','vendor_pending','settled','failed','cancelled'));

ALTER TABLE payout_requests DROP COLUMN fee_quote_id;
ALTER TABLE payout_requests DROP COLUMN fee_amount;
ALTER TABLE payout_requests DROP COLUMN fee_gateway;
