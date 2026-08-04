-- Plan 57 T6: merchant-owned pay-ins. Mirrors the sentinel-zero-UUID
-- convention already established by services/ledger's own
-- Command.MerchantTenantID (Plan 57 T5) rather than adding a separate
-- owner_type discriminator column: a row is merchant-owned when
-- merchant_tenant_id is non-zero, user-owned when it is the zero UUID.
-- DEFAULT '00000000-0000-0000-0000-000000000000' means every existing row
-- backfills for free — no data migration pass required.
ALTER TABLE payin_topup_intents
    ADD COLUMN merchant_tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';
ALTER TABLE payin_topup_intents
    ADD CONSTRAINT chk_topup_owner_shape CHECK (
        (user_id != '00000000-0000-0000-0000-000000000000' AND merchant_tenant_id = '00000000-0000-0000-0000-000000000000')
        OR (user_id = '00000000-0000-0000-0000-000000000000' AND merchant_tenant_id != '00000000-0000-0000-0000-000000000000')
    );

ALTER TABLE payin_webhook_events
    ADD COLUMN merchant_tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';
-- No owner-shape CHECK on payin_webhook_events: a callback that hasn't
-- matched any intent yet has NEITHER a user_id NOR a merchant_tenant_id
-- (both remain the zero sentinel) — see payin.go's HandleVendorCallback,
-- which only fills in the owner once intent-matching succeeds. This is an
-- existing, deliberate state (user_id has been nullable-in-practice since
-- migration 000013), not new to T6.

CREATE INDEX idx_payin_topup_intents_merchant_tenant
    ON payin_topup_intents(merchant_tenant_id)
    WHERE merchant_tenant_id != '00000000-0000-0000-0000-000000000000';
CREATE INDEX idx_payin_webhook_events_merchant_tenant
    ON payin_webhook_events(merchant_tenant_id)
    WHERE merchant_tenant_id != '00000000-0000-0000-0000-000000000000';
