-- Plan 57 T7: SSRF validation is exempt for sandbox endpoints only
-- (docs/reference/c1-b2b-design.md §4 failure matrix: "SSRF validation
-- before dispatch (live mode only)") — a sandbox tenant may legitimately
-- point a webhook at a local receiver for testing. environment is fixed
-- at endpoint creation (mirrors merchant_api_keys.environment, never
-- changed by UpdateEndpoint) and denormalized here rather than joined
-- from merchant_tenants at dispatch time, since the relay's hot path
-- must never add a second table read per delivery just to decide whether
-- to run the SSRF check.
ALTER TABLE merchant_webhook_endpoints
    ADD COLUMN environment TEXT NOT NULL DEFAULT 'live' CHECK (environment IN ('sandbox', 'live'));
