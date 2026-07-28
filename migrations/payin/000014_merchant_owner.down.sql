DROP INDEX IF EXISTS idx_payin_webhook_events_merchant_tenant;
DROP INDEX IF EXISTS idx_payin_topup_intents_merchant_tenant;

ALTER TABLE payin_webhook_events DROP COLUMN merchant_tenant_id;

ALTER TABLE payin_topup_intents DROP CONSTRAINT chk_topup_owner_shape;
ALTER TABLE payin_topup_intents DROP COLUMN merchant_tenant_id;
