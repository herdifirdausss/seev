ALTER TABLE payout_vendor_calls RENAME COLUMN payout_request_id TO request_id;

ALTER TABLE payout_requests DROP COLUMN request_id;
