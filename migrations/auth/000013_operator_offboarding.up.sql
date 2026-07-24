-- docs/roadmap/active/51-a8-data-lifecycle-privacy.md T5's own work item 2
-- (K10): "Admin/operator accounts cannot use self-service closure; they
-- require the operator offboarding runbook and maker/checker approval" —
-- A8 T5b closes that gap. Mirrors ledger's own pending_adjustments
-- maker-checker shape (migrations/ledger/000006) exactly: a maker proposes,
-- a DIFFERENT identity (checker) approves, enforced at both the
-- application layer AND this table's own CHECK constraint as the backstop.
--
-- Approval does not itself pseudonymize anything — it creates the SAME
-- privacy_requests 'closure' row RequestClosure creates (T5), so the
-- existing closure saga worker and every registered owner (A8 T4b/T5b)
-- drive the rest identically to a self-service closure. This table only
-- gates WHO may start that saga for an admin/operator account and records
-- the two-person decision.
CREATE TABLE auth_operator_offboarding_requests (
    id                  UUID        PRIMARY KEY,
    target_user_id      UUID        NOT NULL REFERENCES auth_users(id),
    requested_by        TEXT        NOT NULL,
    approved_by         TEXT        NULL,
    reason              TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'approved', 'rejected')),
    closure_request_id  UUID        NULL REFERENCES privacy_requests(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at          TIMESTAMPTZ NULL,

    CHECK (approved_by IS NULL OR approved_by <> requested_by)
);

CREATE INDEX idx_operator_offboarding_status ON auth_operator_offboarding_requests(status, created_at);

-- At most one pending offboarding proposal per target — mirrors
-- uq_privacy_requests_active_per_user's own "one active request" shape.
CREATE UNIQUE INDEX uq_operator_offboarding_pending_per_target
    ON auth_operator_offboarding_requests (target_user_id)
    WHERE status = 'pending';

GRANT SELECT, INSERT, UPDATE ON auth_operator_offboarding_requests TO app_service;
GRANT SELECT ON auth_operator_offboarding_requests TO app_readonly;

ALTER TABLE auth_operator_offboarding_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_operator_offboarding_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service   ON auth_operator_offboarding_requests FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON auth_operator_offboarding_requests FOR SELECT TO app_readonly USING (true);
