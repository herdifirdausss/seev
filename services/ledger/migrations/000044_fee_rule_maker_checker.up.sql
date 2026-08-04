-- Fee configuration is versioned and approved independently from the maker.
-- fee_rules remains the active projection consumed by existing quote code;
-- only the approval transaction may promote a version into that projection.
ALTER TABLE fee_rules
    ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT 'legacy-bootstrap',
    ADD COLUMN IF NOT EXISTS approved_by TEXT NOT NULL DEFAULT 'legacy-approval',
    ADD COLUMN IF NOT EXISTS rule_version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS effective_until TIMESTAMPTZ NULL,
    ADD CONSTRAINT chk_fee_rules_flat_nonnegative CHECK (flat_minor_units >= 0),
    ADD CONSTRAINT chk_fee_rules_status CHECK (status IN ('active','retired')),
    ADD CONSTRAINT chk_fee_rules_approved_distinct CHECK (approved_by <> created_by);

CREATE TABLE fee_rule_versions (
    id                 UUID PRIMARY KEY,
    rule_id            UUID NOT NULL,
    version            BIGINT NOT NULL CHECK (version > 0),
    tx_type            TEXT NOT NULL,
    gateway            TEXT NOT NULL DEFAULT '',
    currency           TEXT NOT NULL,
    user_id            UUID NULL,
    flat_minor_units   BIGINT NOT NULL DEFAULT 0 CHECK (flat_minor_units >= 0),
    percent_basis_pts  BIGINT NOT NULL DEFAULT 0 CHECK (percent_basis_pts >= 0 AND percent_basis_pts < 10000),
    fee_gateway        TEXT NOT NULL DEFAULT 'platform',
    enabled            BOOLEAN NOT NULL DEFAULT true,
    status             TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft','submitted','approved','rejected','retired')),
    created_by         TEXT NOT NULL,
    submitted_by       TEXT NULL,
    approved_by        TEXT NULL,
    rejected_by        TEXT NULL,
    decision_reason    TEXT NOT NULL DEFAULT '',
    effective_from     TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_until    TIMESTAMPTZ NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (rule_id, version),
    CHECK (approved_by IS NULL OR approved_by <> created_by),
    CHECK (status <> 'approved' OR approved_by IS NOT NULL),
    CHECK (effective_until IS NULL OR effective_until > effective_from)
);

CREATE INDEX idx_fee_rule_versions_scope ON fee_rule_versions(tx_type, currency, status, effective_from);
CREATE INDEX idx_fee_rule_versions_rule ON fee_rule_versions(rule_id, version DESC);

-- Serialize activation for one pricing scope and reject overlapping active
-- windows. The check is deliberately in PostgreSQL, where concurrent admin
-- approvals cannot race a process-local validation.
CREATE OR REPLACE FUNCTION fn_validate_fee_rule_version_overlap() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'approved' THEN
        PERFORM pg_advisory_xact_lock(hashtextextended(
            concat_ws('|', NEW.tx_type, NEW.gateway, NEW.currency, coalesce(NEW.user_id::text, '')), 0));
        IF EXISTS (
            SELECT 1 FROM fee_rule_versions existing
            WHERE existing.status = 'approved'
              AND existing.id <> NEW.id
              AND existing.tx_type = NEW.tx_type
              AND existing.gateway = NEW.gateway
              AND existing.currency = NEW.currency
              AND existing.user_id IS NOT DISTINCT FROM NEW.user_id
              AND tstzrange(existing.effective_from, coalesce(existing.effective_until, 'infinity'::timestamptz), '[)')
                  && tstzrange(NEW.effective_from, coalesce(NEW.effective_until, 'infinity'::timestamptz), '[)')
        ) THEN
            RAISE EXCEPTION 'active fee rule version overlaps an existing version';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_fee_rule_version_overlap
    BEFORE INSERT OR UPDATE ON fee_rule_versions
    FOR EACH ROW EXECUTE FUNCTION fn_validate_fee_rule_version_overlap();

-- Backfill existing active rules as version one, approved by a distinct
-- migration identity. This preserves historical pricing while preventing a
-- new draft from becoming active without a checker.
INSERT INTO fee_rule_versions
    (id, rule_id, version, tx_type, gateway, currency, user_id, flat_minor_units,
     percent_basis_pts, fee_gateway, enabled, status, created_by, approved_by,
     effective_from, created_at, updated_at)
SELECT gen_random_uuid(), id, rule_version, tx_type, gateway, currency, user_id,
       flat_minor_units, percent_basis_pts, fee_gateway, enabled, 'approved',
       created_by, approved_by, effective_from, created_at, updated_at
FROM fee_rules
WHERE NOT EXISTS (SELECT 1 FROM fee_rule_versions v WHERE v.rule_id = fee_rules.id);

GRANT SELECT, INSERT, UPDATE ON fee_rule_versions TO app_service;
GRANT SELECT ON fee_rule_versions TO app_readonly;
ALTER TABLE fee_rule_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE fee_rule_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_fee_rule_versions_service ON fee_rule_versions FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_fee_rule_versions_read_only ON fee_rule_versions FOR SELECT TO app_readonly USING (true);
