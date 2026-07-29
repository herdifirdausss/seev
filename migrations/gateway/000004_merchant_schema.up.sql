-- docs/roadmap/active/57-c1-merchant-b2b-api.md T2 (§11): Gateway-owned
-- persistence for the Merchant/B2B API. All ten tables live in Gateway's
-- own database (seev_gateway) — no cross-service foreign key exists
-- anywhere here; primary_account_id/tenant_id references to
-- LedgerService/PayinService/PayoutService resources are plain UUID
-- columns resolved at the application layer only (§11.1's own note).
-- IDs are supplied by application code (generalutil.NewV7()), matching
-- every other table in this repository — no DEFAULT gen_random_uuid().
-- Every TIMESTAMPTZ column stores UTC internally by Postgres's own type
-- semantics.

CREATE TABLE merchant_tenants (
    id                  UUID        PRIMARY KEY,
    public_id           TEXT        UNIQUE NOT NULL,
    external_code       TEXT        UNIQUE NOT NULL,
    name                TEXT        NOT NULL,
    environment         TEXT        NOT NULL CHECK (environment IN ('sandbox', 'live')),
    status              TEXT        NOT NULL CHECK (status IN ('draft', 'active', 'suspended', 'closed')),
    default_currency    TEXT        NOT NULL,
    primary_account_id  UUID        NULL,
    created_by          TEXT        NOT NULL,
    activated_by        TEXT        NULL,
    suspended_by        TEXT        NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    activated_at        TIMESTAMPTZ NULL,
    suspended_at        TIMESTAMPTZ NULL,
    closed_at           TIMESTAMPTZ NULL
);

CREATE INDEX idx_merchant_tenants_status_env ON merchant_tenants(status, environment);

CREATE TABLE merchant_api_keys (
    id             UUID        PRIMARY KEY,
    public_id      TEXT        UNIQUE NOT NULL,
    tenant_id      UUID        NOT NULL REFERENCES merchant_tenants(id),
    public_prefix  TEXT        UNIQUE NOT NULL,
    secret_digest  BYTEA       NOT NULL,
    environment    TEXT        NOT NULL CHECK (environment IN ('sandbox', 'live')),
    status         TEXT        NOT NULL CHECK (status IN ('active', 'expired', 'revoked')),
    expires_at     TIMESTAMPTZ NULL,
    last_used_at   TIMESTAMPTZ NULL,
    created_by     TEXT        NOT NULL,
    revoked_by     TEXT        NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at     TIMESTAMPTZ NULL
);

-- T3 §8.3's lookup flow: fetch active candidate(s) by prefix, then digest
-- compare — this partial index is the query this whole flow depends on.
CREATE INDEX idx_merchant_api_keys_prefix_active ON merchant_api_keys(public_prefix) WHERE status = 'active';
CREATE INDEX idx_merchant_api_keys_tenant_active ON merchant_api_keys(tenant_id) WHERE status = 'active';

CREATE TABLE merchant_api_key_scopes (
    key_id  UUID NOT NULL REFERENCES merchant_api_keys(id),
    scope   TEXT NOT NULL,
    PRIMARY KEY (key_id, scope)
);

CREATE TABLE merchant_quota_policies (
    id                   UUID        PRIMARY KEY,
    tenant_id            UUID        NOT NULL REFERENCES merchant_tenants(id),
    quota_class          TEXT        NOT NULL,
    requests_per_minute  INTEGER     NOT NULL CHECK (requests_per_minute > 0),
    burst                INTEGER     NOT NULL CHECK (burst > 0),
    is_enabled           BOOLEAN     NOT NULL DEFAULT true,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, quota_class)
);

CREATE TABLE merchant_idempotency_records (
    id                UUID        PRIMARY KEY,
    tenant_id         UUID        NOT NULL REFERENCES merchant_tenants(id),
    operation_id      TEXT        NOT NULL,
    idempotency_key   TEXT        NOT NULL,
    request_hash      BYTEA       NOT NULL,
    downstream_key    TEXT        NOT NULL,
    state             TEXT        NOT NULL CHECK (state IN ('processing', 'completed', 'failed')),
    resource_id       TEXT        NULL,
    http_status       INTEGER     NULL,
    response_body     JSONB       NULL,
    response_headers  JSONB       NULL,
    error_code        TEXT        NULL,
    lease_owner       TEXT        NULL,
    lease_expires_at  TIMESTAMPTZ NULL,
    expires_at        TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- T4 acceptance: "no tenant can collide with another tenant's
    -- idempotency key" — tenant_id is part of the unique key itself, not
    -- merely part of the lookup WHERE clause, so this is a database-level
    -- guarantee, not just an application convention.
    UNIQUE (tenant_id, operation_id, idempotency_key)
);

CREATE INDEX idx_merchant_idempotency_expiry ON merchant_idempotency_records(expires_at);
CREATE INDEX idx_merchant_idempotency_lease_expiry ON merchant_idempotency_records(lease_expires_at) WHERE state = 'processing';

CREATE TABLE merchant_event_inbox (
    event_id          UUID        PRIMARY KEY,
    event_type        TEXT        NOT NULL,
    source            TEXT        NOT NULL,
    payload_hash      BYTEA       NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at      TIMESTAMPTZ NULL,
    processing_error  TEXT        NULL
);

CREATE INDEX idx_merchant_event_inbox_unprocessed ON merchant_event_inbox(received_at) WHERE processed_at IS NULL;

CREATE TABLE merchant_webhook_endpoints (
    id                  UUID        PRIMARY KEY,
    public_id           TEXT        UNIQUE NOT NULL,
    tenant_id           UUID        NOT NULL REFERENCES merchant_tenants(id),
    url                 TEXT        NOT NULL,
    status              TEXT        NOT NULL CHECK (status IN ('enabled', 'disabled')),
    secret_ciphertext   BYTEA       NOT NULL,
    secret_version      INTEGER     NOT NULL,
    subscribed_events   TEXT[]      NOT NULL,
    description         TEXT        NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    disabled_at         TIMESTAMPTZ NULL
);

CREATE INDEX idx_merchant_webhook_endpoints_tenant_status ON merchant_webhook_endpoints(tenant_id, status);

CREATE TABLE merchant_webhook_events (
    id               UUID        PRIMARY KEY,
    public_id        TEXT        UNIQUE NOT NULL,
    tenant_id        UUID        NOT NULL REFERENCES merchant_tenants(id),
    event_type       TEXT        NOT NULL,
    schema_version   INTEGER     NOT NULL,
    livemode         BOOLEAN     NOT NULL,
    payload          JSONB       NOT NULL,
    payload_bytes    BYTEA       NOT NULL,
    source_event_id  UUID        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source_event_id, event_type)
);

CREATE INDEX idx_merchant_webhook_events_source ON merchant_webhook_events(source_event_id);

CREATE TABLE merchant_webhook_deliveries (
    id                     UUID        PRIMARY KEY,
    public_id              TEXT        UNIQUE NOT NULL,
    tenant_id              UUID        NOT NULL REFERENCES merchant_tenants(id),
    endpoint_id            UUID        NOT NULL REFERENCES merchant_webhook_endpoints(id),
    event_id               UUID        NOT NULL REFERENCES merchant_webhook_events(id),
    -- NULL for the automatic delivery; set to that delivery's id for every
    -- replay row (T7: "replay creates a new delivery ID with the same
    -- event ID"). This is what lets the partial unique index below allow
    -- unlimited replays while still bounding the AUTOMATIC path to one row.
    replay_of_delivery_id  UUID        NULL REFERENCES merchant_webhook_deliveries(id),
    status                 TEXT        NOT NULL CHECK (status IN ('pending', 'delivered', 'failed', 'dead')),
    attempt_count          INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at        TIMESTAMPTZ NULL,
    lease_owner            TEXT        NULL,
    lease_expires_at       TIMESTAMPTZ NULL,
    last_http_status       INTEGER     NULL,
    last_error_code        TEXT        NULL,
    first_attempt_at       TIMESTAMPTZ NULL,
    delivered_at           TIMESTAMPTZ NULL,
    dead_at                TIMESTAMPTZ NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One endpoint gets at most one AUTOMATIC delivery record per event (T7
-- acceptance) — a partial index scoped to replay_of_delivery_id IS NULL,
-- so every replay row (which always sets replay_of_delivery_id) is exempt
-- by construction, not by a comment claiming a plain UNIQUE "doesn't
-- apply" to it (an earlier draft of this migration got exactly that wrong
-- — a plain UNIQUE(endpoint_id, event_id) rejected every replay insert
-- outright, caught by this table's own integration test).
CREATE UNIQUE INDEX idx_merchant_webhook_deliveries_automatic_unique
    ON merchant_webhook_deliveries(endpoint_id, event_id) WHERE replay_of_delivery_id IS NULL;

CREATE INDEX idx_merchant_webhook_deliveries_due ON merchant_webhook_deliveries(next_attempt_at) WHERE status IN ('pending', 'failed');
CREATE INDEX idx_merchant_webhook_deliveries_lease ON merchant_webhook_deliveries(lease_expires_at) WHERE lease_owner IS NOT NULL;
CREATE INDEX idx_merchant_webhook_deliveries_tenant_cursor ON merchant_webhook_deliveries(tenant_id, created_at DESC);

CREATE TABLE merchant_webhook_attempts (
    id                UUID        PRIMARY KEY,
    delivery_id       UUID        NOT NULL REFERENCES merchant_webhook_deliveries(id) ON DELETE CASCADE,
    attempt_number    INTEGER     NOT NULL CHECK (attempt_number > 0),
    started_at        TIMESTAMPTZ NOT NULL,
    finished_at       TIMESTAMPTZ NOT NULL,
    http_status       INTEGER     NULL,
    duration_ms       INTEGER     NOT NULL CHECK (duration_ms >= 0),
    error_code        TEXT        NULL,
    response_excerpt  TEXT        NULL,
    UNIQUE (delivery_id, attempt_number)
);

CREATE INDEX idx_merchant_webhook_attempts_delivery ON merchant_webhook_attempts(delivery_id);

GRANT SELECT, INSERT, UPDATE ON merchant_tenants TO app_service;
GRANT SELECT, INSERT, UPDATE ON merchant_api_keys TO app_service;
GRANT SELECT, INSERT, DELETE ON merchant_api_key_scopes TO app_service;
GRANT SELECT, INSERT, UPDATE ON merchant_quota_policies TO app_service;
GRANT SELECT, INSERT, UPDATE, DELETE ON merchant_idempotency_records TO app_service;
GRANT SELECT, INSERT, UPDATE, DELETE ON merchant_event_inbox TO app_service;
GRANT SELECT, INSERT, UPDATE, DELETE ON merchant_webhook_endpoints TO app_service;
GRANT SELECT, INSERT, DELETE ON merchant_webhook_events TO app_service;
GRANT SELECT, INSERT, UPDATE, DELETE ON merchant_webhook_deliveries TO app_service;
GRANT SELECT, INSERT, DELETE ON merchant_webhook_attempts TO app_service;

GRANT SELECT ON merchant_tenants, merchant_api_keys, merchant_api_key_scopes,
    merchant_quota_policies, merchant_idempotency_records, merchant_event_inbox,
    merchant_webhook_endpoints, merchant_webhook_events, merchant_webhook_deliveries,
    merchant_webhook_attempts TO app_readonly;

ALTER TABLE merchant_tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_api_key_scopes ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_quota_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_idempotency_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_event_inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_webhook_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_webhook_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_webhook_attempts ENABLE ROW LEVEL SECURITY;

ALTER TABLE merchant_tenants FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_api_keys FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_api_key_scopes FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_quota_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_idempotency_records FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_event_inbox FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_webhook_endpoints FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_webhook_events FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_webhook_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE merchant_webhook_attempts FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_merchant_tenants_service ON merchant_tenants FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_tenants_readonly ON merchant_tenants FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_api_keys_service ON merchant_api_keys FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_api_keys_readonly ON merchant_api_keys FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_api_key_scopes_service ON merchant_api_key_scopes FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_api_key_scopes_readonly ON merchant_api_key_scopes FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_quota_policies_service ON merchant_quota_policies FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_quota_policies_readonly ON merchant_quota_policies FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_idempotency_records_service ON merchant_idempotency_records FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_idempotency_records_readonly ON merchant_idempotency_records FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_event_inbox_service ON merchant_event_inbox FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_event_inbox_readonly ON merchant_event_inbox FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_webhook_endpoints_service ON merchant_webhook_endpoints FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_webhook_endpoints_readonly ON merchant_webhook_endpoints FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_webhook_events_service ON merchant_webhook_events FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_webhook_events_readonly ON merchant_webhook_events FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_webhook_deliveries_service ON merchant_webhook_deliveries FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_webhook_deliveries_readonly ON merchant_webhook_deliveries FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_merchant_webhook_attempts_service ON merchant_webhook_attempts FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_webhook_attempts_readonly ON merchant_webhook_attempts FOR SELECT TO app_readonly USING (true);
