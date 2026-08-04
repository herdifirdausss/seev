-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md K5: local retention holds,
-- vendor's copy of the shape every owner service already carries (see e.g.
-- services/fraud/migrations/000005_retention_holds.up.sql) — vendor-service never
-- got this migration when it was extracted from payin (Plan 54), which is
-- why config/data-retention.yaml's vendor.callback_inbox/vendor.outbound_attempts
-- entries were declared but never wired to a running retentionworker.Runner
-- (discovered live via a load-test soak: vendor_callback_inbox grew
-- unbounded with no purge ever executing against it).
CREATE TABLE vendor_retention_holds (
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

    CONSTRAINT chk_vendor_retention_hold_release_fields CHECK (
        (status = 'active'   AND released_by IS NULL     AND released_at IS NULL) OR
        (status = 'released' AND released_by IS NOT NULL AND released_at IS NOT NULL)
    ),
    CONSTRAINT chk_vendor_retention_hold_releaser_not_creator CHECK (
        released_by IS NULL OR released_by <> created_by
    )
);

CREATE INDEX idx_vendor_retention_holds_scope  ON vendor_retention_holds(scope, scope_value) WHERE status = 'active';
CREATE INDEX idx_vendor_retention_holds_expiry ON vendor_retention_holds(expires_at) WHERE status = 'active' AND expires_at IS NOT NULL;

CREATE TABLE vendor_retention_audit (
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

CREATE INDEX idx_vendor_retention_audit_class ON vendor_retention_audit(class, finished_at DESC);
CREATE INDEX idx_vendor_retention_audit_job   ON vendor_retention_audit(job_id);

GRANT SELECT, INSERT, UPDATE ON vendor_retention_holds TO app_service;
GRANT SELECT, INSERT ON vendor_retention_audit TO app_service;
GRANT SELECT ON vendor_retention_holds, vendor_retention_audit TO app_readonly;

ALTER TABLE vendor_retention_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_retention_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_retention_holds FORCE ROW LEVEL SECURITY;
ALTER TABLE vendor_retention_audit FORCE ROW LEVEL SECURITY;

CREATE POLICY vendor_retention_holds_service  ON vendor_retention_holds FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY vendor_retention_holds_readonly ON vendor_retention_holds FOR SELECT TO app_readonly USING (true);
CREATE POLICY vendor_retention_audit_service  ON vendor_retention_audit FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY vendor_retention_audit_readonly ON vendor_retention_audit FOR SELECT TO app_readonly USING (true);

-- vendor.callback_inbox and vendor.outbound_attempts are both declared
-- hold_scope: none in config/data-retention.yaml (a callback/outbound-call
-- row has no subject/resource a legal hold could target), so neither
-- purge function calls this — created anyway for framework consistency
-- (retentionworker.Runner.refreshHoldsGauge queries <owner>_retention_holds
-- unconditionally) and in case a future vendor class needs it.
CREATE OR REPLACE FUNCTION fn_vendor_retention_hold_covers(
    p_table    TEXT,
    p_subject  TEXT,
    p_resource TEXT,
    p_at       TIMESTAMPTZ DEFAULT now()
) RETURNS BOOLEAN
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
    SELECT EXISTS (
        SELECT 1 FROM vendor_retention_holds h
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

GRANT EXECUTE ON FUNCTION fn_vendor_retention_hold_covers(TEXT, TEXT, TEXT, TIMESTAMPTZ) TO app_service;
