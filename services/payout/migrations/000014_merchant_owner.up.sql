-- Plan 57 T6: merchant-owned payouts. Same sentinel-zero-UUID convention
-- as services/payin/migrations/000014 — a row is merchant-owned when
-- merchant_tenant_id is non-zero, user-owned when it is the zero UUID.
-- DEFAULT '00000000-0000-0000-0000-000000000000' backfills every existing
-- row for free — no data migration pass required.
ALTER TABLE payout_requests
    ADD COLUMN merchant_tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';
ALTER TABLE payout_requests
    ADD CONSTRAINT chk_payout_owner_shape CHECK (
        (user_id != '00000000-0000-0000-0000-000000000000' AND merchant_tenant_id = '00000000-0000-0000-0000-000000000000')
        OR (user_id = '00000000-0000-0000-0000-000000000000' AND merchant_tenant_id != '00000000-0000-0000-0000-000000000000')
    );

CREATE INDEX idx_payout_requests_merchant_tenant
    ON payout_requests(merchant_tenant_id, created_at DESC)
    WHERE merchant_tenant_id != '00000000-0000-0000-0000-000000000000';
