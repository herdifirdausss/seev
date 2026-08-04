-- Plan 57 (B2B HTTP handlers follow-up): CreateMerchant needs to be
-- idempotent against a Gateway retry after a crash between the owner call
-- succeeding and the Gateway idempotency record being persisted
-- (docs/reference/c1-b2b-design.md §10.4/failure matrix — "Gateway crash
-- after owner-service success" must recover the ORIGINAL resource, not
-- create a second one). downstream_key is the deterministic key Gateway's
-- own idempotency.DownstreamKey derives per (tenant, operation, merchant
-- idempotency key); NULL for every user-owned row (they never carry one).
ALTER TABLE payout_requests
    ADD COLUMN downstream_key TEXT NULL;

CREATE UNIQUE INDEX idx_payout_requests_merchant_downstream_key
    ON payout_requests(merchant_tenant_id, downstream_key)
    WHERE downstream_key IS NOT NULL;
