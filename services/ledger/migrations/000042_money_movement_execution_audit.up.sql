-- Every command entering the shared money-movement boundary records its
-- preflight decision. The table is append-only evidence, not a mutable policy
-- cache. Amounts are minor units and the idempotency key itself is never
-- stored; correlation IDs and the ledger transaction provide linkage without
-- retaining a replay secret.
CREATE TABLE money_movement_policy_decisions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id           UUID,
    tenant_id          UUID,
    user_id            UUID,
    source             TEXT NOT NULL,
    correlation_id     TEXT NOT NULL,
    request_origin     TEXT NOT NULL,
    transaction_type   TEXT NOT NULL,
    currency           CHAR(3),
    amount_minor       BIGINT NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    allowed            BOOLEAN NOT NULL,
    reason             TEXT NOT NULL DEFAULT '',
    detail             TEXT NOT NULL DEFAULT '',
    effective_at       TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_money_movement_policy_decisions_lookup
    ON money_movement_policy_decisions (user_id, created_at DESC);
CREATE INDEX idx_money_movement_policy_decisions_denials
    ON money_movement_policy_decisions (source, reason, created_at DESC)
    WHERE NOT allowed;

REVOKE UPDATE, DELETE, TRUNCATE ON money_movement_policy_decisions FROM app_service;
GRANT SELECT, INSERT ON money_movement_policy_decisions TO app_service;
GRANT SELECT ON money_movement_policy_decisions TO app_readonly;
ALTER TABLE money_movement_policy_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_movement_policy_decisions FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_policy_decision_service ON money_movement_policy_decisions
    FOR INSERT TO app_service WITH CHECK (true);
CREATE POLICY pol_policy_decision_read_service ON money_movement_policy_decisions
    FOR SELECT TO app_service USING (true);
CREATE POLICY pol_policy_decision_read_only ON money_movement_policy_decisions
    FOR SELECT TO app_readonly USING (true);
