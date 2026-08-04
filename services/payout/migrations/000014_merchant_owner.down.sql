DROP INDEX IF EXISTS idx_payout_requests_merchant_tenant;
ALTER TABLE payout_requests DROP CONSTRAINT chk_payout_owner_shape;
ALTER TABLE payout_requests DROP COLUMN merchant_tenant_id;
