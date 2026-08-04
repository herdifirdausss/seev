-- docs/roadmap/active/57-c1-merchant-b2b-api.md T8 §16.3: "live-mode
-- activation: checker" and "tenant closure: checker" — a maker proposes,
-- a DIFFERENT identity (checker) approves, enforced at both the
-- application layer AND this table's own CHECK constraint as the
-- backstop. Mirrors auth_operator_offboarding_requests
-- (services/auth/migrations/000013) exactly, generalized to two action kinds
-- instead of one hardcoded operation.
CREATE TABLE merchant_tenant_lifecycle_requests (
    id            UUID        PRIMARY KEY,
    tenant_id     UUID        NOT NULL REFERENCES merchant_tenants(id),
    action        TEXT        NOT NULL CHECK (action IN ('activate', 'close')),
    requested_by  TEXT        NOT NULL,
    approved_by   TEXT        NULL,
    reason        TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'approved', 'rejected')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at    TIMESTAMPTZ NULL,

    CHECK (approved_by IS NULL OR approved_by <> requested_by)
);

CREATE INDEX idx_merchant_tenant_lifecycle_status ON merchant_tenant_lifecycle_requests(status, created_at);

-- At most one pending proposal per (tenant, action) at a time.
CREATE UNIQUE INDEX uq_merchant_tenant_lifecycle_pending_per_action
    ON merchant_tenant_lifecycle_requests (tenant_id, action)
    WHERE status = 'pending';

GRANT SELECT, INSERT, UPDATE ON merchant_tenant_lifecycle_requests TO app_service;
GRANT SELECT ON merchant_tenant_lifecycle_requests TO app_readonly;

ALTER TABLE merchant_tenant_lifecycle_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_tenant_lifecycle_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_merchant_tenant_lifecycle_service   ON merchant_tenant_lifecycle_requests FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_tenant_lifecycle_readonly  ON merchant_tenant_lifecycle_requests FOR SELECT TO app_readonly USING (true);
