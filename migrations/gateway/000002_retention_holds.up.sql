-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md K5: local retention holds. Auth
-- coordinates a hold command, but every affected owner service persists its
-- own copy before acknowledging it — this table IS that local copy, and it
-- is what a purge/redact function (see the next K4 migration) checks before
-- acting. Identical shape across all eight owner databases by design (K5).
-- Prefixed with the owning service (gateway_) rather than the unprefixed
-- "retention_holds" every service would otherwise share by coincidence:
-- several of this repo's own integration test suites (internal/testutil's
-- ApplyServiceMigrations) apply every service's migrations into ONE shared
-- throwaway test database (version-tracked per service, but physically
-- merged) — an identical unprefixed name across services collided there
-- (CREATE TABLE retention_holds already exists) even though real
-- deployment never shares one physical database across services. Matches
-- this repo's own existing precedent (payout_requests, payin_webhook_events,
-- notif_notifications) for exactly this situation.
CREATE TABLE gateway_retention_holds (
    id            UUID PRIMARY KEY,
    scope         TEXT NOT NULL CHECK (scope IN ('subject','resource','table','time_range')),
    scope_value   TEXT NOT NULL,
    reason_code   TEXT NOT NULL,
    reason_note   TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NULL,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','released')),
    released_by   TEXT NULL,
    released_at   TIMESTAMPTZ NULL,

    -- release fields are set together, never independently
    CONSTRAINT chk_gateway_retention_hold_release_fields CHECK (
        (status = 'active'   AND released_by IS NULL     AND released_at IS NULL) OR
        (status = 'released' AND released_by IS NOT NULL AND released_at IS NOT NULL)
    ),
    -- K5: releasing a hold requires a DIFFERENT admin/admin_checker than the
    -- one who created it — enforced here too, not only in the Go handler,
    -- since this table is the last line of defense against a bypass.
    CONSTRAINT chk_gateway_retention_hold_releaser_not_creator CHECK (
        released_by IS NULL OR released_by <> created_by
    )
);

CREATE INDEX idx_gateway_retention_holds_scope  ON gateway_retention_holds(scope, scope_value) WHERE status = 'active';
CREATE INDEX idx_gateway_retention_holds_expiry ON gateway_retention_holds(expires_at) WHERE status = 'active' AND expires_at IS NOT NULL;

-- docs/roadmap/archive/51 K4: append-only audit row, written in the SAME transaction as
-- every purge/redact a SECURITY DEFINER retention function performs — never
-- a best-effort side write. No sensitive values (row IDs, field contents),
-- only counts and the policy version that authorized the run.
CREATE TABLE gateway_retention_audit (
    id             BIGSERIAL PRIMARY KEY,
    job_id         UUID NOT NULL,
    class          TEXT NOT NULL,
    action         TEXT NOT NULL,
    dry_run        BOOLEAN NOT NULL,
    affected_count INT NOT NULL CHECK (affected_count >= 0),
    policy_version INT NOT NULL,
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    result         TEXT NOT NULL CHECK (result IN ('ok','error')),
    error_message  TEXT NULL
);

CREATE INDEX idx_gateway_retention_audit_class ON gateway_retention_audit(class, finished_at DESC);
CREATE INDEX idx_gateway_retention_audit_job   ON gateway_retention_audit(job_id);

GRANT SELECT, INSERT, UPDATE ON gateway_retention_holds TO app_service;
GRANT SELECT, INSERT ON gateway_retention_audit TO app_service;
GRANT SELECT ON gateway_retention_holds, gateway_retention_audit TO app_readonly;

ALTER TABLE gateway_retention_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE gateway_retention_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE gateway_retention_holds FORCE ROW LEVEL SECURITY;
ALTER TABLE gateway_retention_audit FORCE ROW LEVEL SECURITY;

CREATE POLICY gateway_retention_holds_service  ON gateway_retention_holds FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY gateway_retention_holds_readonly ON gateway_retention_holds FOR SELECT TO app_readonly USING (true);
CREATE POLICY gateway_retention_audit_service  ON gateway_retention_audit FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY gateway_retention_audit_readonly ON gateway_retention_audit FOR SELECT TO app_readonly USING (true);

-- docs/roadmap/archive/51 K5: shared eligibility helper every purge/redact function
-- (fn_retention_purge_*) calls in its own WHERE clause, so a row's hold
-- coverage is checked atomically inside the same statement that would
-- otherwise delete/redact it — never a separate, race-prone Go-side check.
-- p_subject/p_resource may be NULL when a class has no natural subject or
-- resource identifier; a NULL parameter simply never matches a hold row
-- (scope_value is NOT NULL by the table's own constraint), which is the
-- correct behavior — a hold can't be evaluated against an identifier that
-- doesn't exist for that class.
CREATE OR REPLACE FUNCTION fn_gateway_retention_hold_covers(
    p_table    TEXT,
    p_subject  TEXT,
    p_resource TEXT,
    p_at       TIMESTAMPTZ DEFAULT now()
) RETURNS BOOLEAN
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
    SELECT EXISTS (
        SELECT 1 FROM gateway_retention_holds h
        WHERE h.status = 'active'
          AND (
            (h.scope = 'table'      AND h.scope_value = p_table) OR
            (h.scope = 'subject'    AND p_subject  IS NOT NULL AND h.scope_value = p_subject) OR
            (h.scope = 'resource'   AND p_resource IS NOT NULL AND h.scope_value = p_resource) OR
            (h.scope = 'time_range' AND p_at BETWEEN
                split_part(h.scope_value, ',', 1)::timestamptz
                AND split_part(h.scope_value, ',', 2)::timestamptz)
          )
    );
$$;

GRANT EXECUTE ON FUNCTION fn_gateway_retention_hold_covers(TEXT, TEXT, TEXT, TIMESTAMPTZ) TO app_service;
