-- Plan 57 (B2B HTTP handlers follow-up): CreateMerchantTopupIntent needs to
-- be idempotent against a Gateway retry after a crash between the owner
-- call succeeding and the Gateway idempotency record being persisted
-- (docs/reference/c1-b2b-design.md §10.4/failure matrix — "Gateway crash
-- after owner-service success" must recover the ORIGINAL resource, not
-- create a second one). downstream_key is the deterministic key Gateway's
-- own idempotency.DownstreamKey derives per (tenant, operation, merchant
-- idempotency key); NULL for every user-owned row (they never carry one).
ALTER TABLE payin_topup_intents
    ADD COLUMN downstream_key TEXT NULL;

CREATE UNIQUE INDEX idx_payin_topup_intents_merchant_downstream_key
    ON payin_topup_intents(merchant_tenant_id, downstream_key)
    WHERE downstream_key IS NOT NULL;
