-- C5: monthly-capitalised savings interest and durable scheduled execution.
--
-- This migration is additive.  The legacy savings_config, interest_accrue
-- transaction type, and scheduled_transactions columns remain readable while
-- the new product paths are rolled out behind their own kill switches.

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment','suspense',
     'fx_conversion','interest_expense','accrued_interest_payable'));

ALTER TABLE account_balance_snapshots
    ADD COLUMN IF NOT EXISTS id UUID DEFAULT gen_random_uuid();
UPDATE account_balance_snapshots SET id = gen_random_uuid() WHERE id IS NULL;
ALTER TABLE account_balance_snapshots ALTER COLUMN id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_account_balance_snapshots_id ON account_balance_snapshots(id);

-- Make quote ownership explicit for C5's Payin commitment. Older quotes only
-- carried a prefixed consumed_by_ref, so backfill a conservative type and
-- keep the reference for compatibility with existing retention proof.
ALTER TABLE fee_quotes
    ADD COLUMN IF NOT EXISTS consumed_by_type TEXT;
UPDATE fee_quotes
SET consumed_by_type = CASE
    WHEN consumed_by_ref LIKE 'payin:%' THEN 'payin'
    WHEN consumed_by_ref LIKE 'payout:%' THEN 'payout'
    WHEN consumed_by_ref LIKE 'tx:%' THEN 'transaction'
    ELSE 'legacy'
END
WHERE consumed_by_type IS NULL;
ALTER TABLE fee_quotes
    ALTER COLUMN consumed_by_type SET DEFAULT 'legacy',
    ALTER COLUMN consumed_by_type SET NOT NULL;
ALTER TABLE fee_quotes
    ADD CONSTRAINT fee_quotes_consumed_by_type_check
    CHECK (consumed_by_type IN ('payin','payout','transaction','legacy'));
CREATE INDEX idx_fee_quotes_consumed_by_type
    ON fee_quotes(consumed_by_type, consumed_at)
    WHERE consumed_at IS NOT NULL;

CREATE TABLE savings_products (
    id                              UUID PRIMARY KEY,
    public_id                       TEXT NOT NULL UNIQUE,
    product_code                    TEXT NOT NULL UNIQUE,
    name                            TEXT NOT NULL,
    currency                        CHAR(3) NOT NULL,
    eligible_account_types          TEXT[] NOT NULL DEFAULT ARRAY['cash','pocket']::TEXT[],
    status                          TEXT NOT NULL CHECK (status IN ('draft','active','intake_paused','retired')),
    day_count_convention            TEXT NOT NULL CHECK (day_count_convention = 'ACT/365F'),
    capitalization_frequency        TEXT NOT NULL CHECK (capitalization_frequency = 'monthly'),
    timezone                        TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    minimum_eligible_balance        BIGINT NOT NULL DEFAULT 1 CHECK (minimum_eligible_balance >= 0),
    interest_expense_account_id     UUID NOT NULL REFERENCES accounts(id),
    interest_payable_account_id     UUID NOT NULL REFERENCES accounts(id),
    default_rate_policy             TEXT NOT NULL DEFAULT 'effective_rate_version',
    version                         BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by                      TEXT NOT NULL,
    updated_by                      TEXT NOT NULL,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_savings_products_status ON savings_products(status, currency);

CREATE TABLE savings_rate_versions (
    id                 UUID PRIMARY KEY,
    public_id          TEXT NOT NULL UNIQUE,
    product_id         UUID NOT NULL REFERENCES savings_products(id),
    annual_rate_bps    INTEGER NOT NULL CHECK (annual_rate_bps BETWEEN 0 AND 2000),
    status             TEXT NOT NULL CHECK (status IN ('draft','pending_approval','active','retired','rejected')),
    effective_from    DATE NOT NULL,
    effective_until   DATE NULL,
    content_hash      BYTEA NOT NULL,
    created_by        TEXT NOT NULL,
    submitted_by      TEXT NULL,
    approved_by       TEXT NULL,
    rejected_by       TEXT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at      TIMESTAMPTZ NULL,
    approved_at       TIMESTAMPTZ NULL,
    retired_at        TIMESTAMPTZ NULL,
    rejection_reason  TEXT NULL,
    CHECK (effective_until IS NULL OR effective_until > effective_from),
    CHECK (approved_by IS NULL OR approved_by <> created_by)
);

CREATE INDEX idx_savings_rate_versions_window
    ON savings_rate_versions(product_id, effective_from, effective_until)
    WHERE status = 'active';

CREATE TABLE savings_enrollments (
    id                  UUID PRIMARY KEY,
    public_id           TEXT NOT NULL UNIQUE,
    product_id          UUID NOT NULL REFERENCES savings_products(id),
    account_id          UUID NOT NULL REFERENCES accounts(id),
    user_id             UUID NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('pending','active','accrual_paused','ended')),
    mode                TEXT NOT NULL DEFAULT 'monthly_liability_capitalization'
                        CHECK (mode IN ('legacy_daily_capitalization','monthly_liability_capitalization')),
    effective_from      DATE NOT NULL,
    effective_until     DATE NULL,
    carry_numerator     NUMERIC(78,0) NOT NULL DEFAULT 0 CHECK (carry_numerator >= 0),
    carry_denominator   NUMERIC(78,0) NOT NULL DEFAULT 3650000 CHECK (carry_denominator > 0),
    version             BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by          TEXT NOT NULL,
    updated_by          TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (effective_until IS NULL OR effective_until > effective_from),
    UNIQUE (product_id, account_id, effective_from)
);

CREATE UNIQUE INDEX uq_savings_enrollments_active_product_account
    ON savings_enrollments(product_id, account_id)
    WHERE status = 'active';

CREATE UNIQUE INDEX uq_savings_enrollments_active
    ON savings_enrollments(product_id, account_id)
    WHERE status IN ('pending','active','accrual_paused');
CREATE INDEX idx_savings_enrollments_due
    ON savings_enrollments(effective_from, effective_until, status);

-- Enrollment status changes are calendar-effective evidence.  The current
-- enrollment row is the operational projection; this append-oriented history
-- is what lets period close count only the days that were actually active
-- when an enrollment was paused, resumed, or ended mid-period.
CREATE TABLE savings_enrollment_status_history (
    id              UUID PRIMARY KEY,
    enrollment_id   UUID NOT NULL REFERENCES savings_enrollments(id),
    status          TEXT NOT NULL CHECK (status IN ('pending','active','accrual_paused','ended')),
    effective_from  DATE NOT NULL,
    changed_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (enrollment_id, effective_from)
);

CREATE INDEX idx_savings_enrollment_status_history_lookup
    ON savings_enrollment_status_history(enrollment_id, effective_from DESC);

INSERT INTO savings_enrollment_status_history
    (id, enrollment_id, status, effective_from, changed_by, created_at)
SELECT gen_random_uuid(), id, status, effective_from, created_by, created_at
FROM savings_enrollments
ON CONFLICT (enrollment_id, effective_from) DO NOTHING;

CREATE TABLE interest_periods (
    id                    UUID PRIMARY KEY,
    public_id             TEXT NOT NULL UNIQUE,
    product_id            UUID NOT NULL REFERENCES savings_products(id),
    currency              CHAR(3) NOT NULL,
    period_year           INTEGER NOT NULL CHECK (period_year BETWEEN 2000 AND 9999),
    period_month          INTEGER NOT NULL CHECK (period_month BETWEEN 1 AND 12),
    period_start_at       TIMESTAMPTZ NOT NULL,
    period_end_at         TIMESTAMPTZ NOT NULL,
    accrual_cutoff_at     TIMESTAMPTZ NOT NULL,
    close_not_before_at   TIMESTAMPTZ NOT NULL,
    status                TEXT NOT NULL CHECK (status IN ('planned','open','closing','closed','failed','cancelled_before_open')),
    expected_item_count   BIGINT NOT NULL DEFAULT 0 CHECK (expected_item_count >= 0),
    completed_item_count  BIGINT NOT NULL DEFAULT 0 CHECK (completed_item_count >= 0),
    blocked_item_count    BIGINT NOT NULL DEFAULT 0 CHECK (blocked_item_count >= 0),
    total_accrued_amount  BIGINT NOT NULL DEFAULT 0 CHECK (total_accrued_amount >= 0),
    total_capitalized_amount BIGINT NOT NULL DEFAULT 0 CHECK (total_capitalized_amount >= 0),
    opened_at             TIMESTAMPTZ NULL,
    closing_started_at    TIMESTAMPTZ NULL,
    closed_at             TIMESTAMPTZ NULL,
    failed_at              TIMESTAMPTZ NULL,
    last_error_code       TEXT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (period_end_at > period_start_at),
    UNIQUE (product_id, period_year, period_month)
);

CREATE INDEX idx_interest_periods_due ON interest_periods(status, close_not_before_at);

CREATE TABLE interest_daily_accruals (
    id                       UUID PRIMARY KEY,
    period_id                UUID NOT NULL REFERENCES interest_periods(id),
    enrollment_id            UUID NOT NULL REFERENCES savings_enrollments(id),
    account_id               UUID NOT NULL REFERENCES accounts(id),
    accrual_date             DATE NOT NULL,
    snapshot_id              UUID NULL REFERENCES account_balance_snapshots(id),
    closing_balance          BIGINT NULL,
    rate_version_id          UUID NULL REFERENCES savings_rate_versions(id),
    annual_rate_bps          INTEGER NULL,
    exact_numerator          NUMERIC(78,0) NULL,
    denominator              NUMERIC(78,0) NULL,
    opening_carry_numerator  NUMERIC(78,0) NULL,
    recognized_amount        BIGINT NULL CHECK (recognized_amount IS NULL OR recognized_amount >= 0),
    closing_carry_numerator  NUMERIC(78,0) NULL,
    status                   TEXT NOT NULL CHECK (status IN ('pending','processing','completed_zero','completed_posted','retry_wait','blocked','failed','adjusted')),
    attempt_count            INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at          TIMESTAMPTZ NULL,
    lease_owner              TEXT NULL,
    lease_expires_at         TIMESTAMPTZ NULL,
    ledger_transaction_id    UUID NULL,
    error_code               TEXT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (enrollment_id, accrual_date)
);

CREATE INDEX idx_interest_daily_accruals_work
    ON interest_daily_accruals(status, next_attempt_at, accrual_date);
CREATE INDEX idx_interest_daily_accruals_period
    ON interest_daily_accruals(period_id, enrollment_id, accrual_date);

CREATE TABLE interest_capitalization_items (
    id                    UUID PRIMARY KEY,
    period_id             UUID NOT NULL REFERENCES interest_periods(id),
    enrollment_id         UUID NOT NULL REFERENCES savings_enrollments(id),
    account_id            UUID NOT NULL REFERENCES accounts(id),
    capitalization_amount BIGINT NOT NULL CHECK (capitalization_amount >= 0),
    status                TEXT NOT NULL CHECK (status IN ('pending','processing','posted','completed_zero','retry_wait','blocked','failed','adjusted')),
    attempt_count         INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at       TIMESTAMPTZ NULL,
    lease_owner           TEXT NULL,
    lease_expires_at      TIMESTAMPTZ NULL,
    ledger_transaction_id UUID NULL,
    error_code            TEXT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (period_id, enrollment_id)
);

CREATE INDEX idx_interest_capitalization_work
    ON interest_capitalization_items(status, next_attempt_at, period_id);

CREATE TABLE interest_period_checks (
    id              UUID PRIMARY KEY,
    period_id       UUID NOT NULL REFERENCES interest_periods(id),
    check_name      TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pass','fail','warning','not_run')),
    expected_value  TEXT NULL,
    actual_value    TEXT NULL,
    severity        TEXT NOT NULL CHECK (severity IN ('info','warning','critical')),
    details         JSONB NULL,
    checked_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (period_id, check_name)
);

CREATE TABLE interest_adjustments (
    id                       UUID PRIMARY KEY,
    public_id                TEXT NOT NULL UNIQUE,
    source_period_id         UUID NOT NULL REFERENCES interest_periods(id),
    enrollment_id            UUID NOT NULL REFERENCES savings_enrollments(id),
    source_accrual_id        UUID NULL REFERENCES interest_daily_accruals(id),
    source_capitalization_id UUID NULL REFERENCES interest_capitalization_items(id),
    amount                   BIGINT NOT NULL CHECK (amount > 0),
    direction                TEXT NOT NULL CHECK (direction IN ('positive','negative')),
    status                   TEXT NOT NULL CHECK (status IN ('draft','pending_approval','approved','rejected','posted')),
    reason                   TEXT NOT NULL,
    created_by               TEXT NOT NULL,
    approved_by              TEXT NULL,
    ledger_transaction_id    UUID NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_at              TIMESTAMPTZ NULL,
    posted_at                TIMESTAMPTZ NULL,
    CHECK (approved_by IS NULL OR approved_by <> created_by)
);

-- The old schedule row remains the compatibility definition.  New fields are
-- normalized copies used by the planner/evaluator; old cmd_payload remains
-- available for historical reads during the expand/contract rollout.
ALTER TABLE scheduled_transactions
    ADD COLUMN IF NOT EXISTS command_type TEXT,
    ADD COLUMN IF NOT EXISTS command_version INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS command_digest BYTEA,
    ADD COLUMN IF NOT EXISTS currency CHAR(3),
    ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    ADD COLUMN IF NOT EXISTS local_time TIME NOT NULL DEFAULT '00:30',
    ADD COLUMN IF NOT EXISTS missed_run_policy TEXT,
    ADD COLUMN IF NOT EXISTS catch_up_limit INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS max_fee_amount BIGINT,
    ADD COLUMN IF NOT EXISTS max_infrastructure_attempts INTEGER NOT NULL DEFAULT 5,
    ADD COLUMN IF NOT EXISTS retry_window_seconds BIGINT NOT NULL DEFAULT 86400,
    ADD COLUMN IF NOT EXISTS fee_mode TEXT NOT NULL DEFAULT 'current_policy_with_consent_cap',
    ADD COLUMN IF NOT EXISTS consecutive_failure_threshold INTEGER NOT NULL DEFAULT 3,
    ADD COLUMN IF NOT EXISTS consecutive_failure_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_planned_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS paused_reason TEXT;

UPDATE scheduled_transactions
SET command_type = COALESCE(command_type, cmd_payload->>'type'),
    command_digest = COALESCE(command_digest, decode(md5(cmd_payload::text), 'hex')),
    missed_run_policy = COALESCE(missed_run_policy,
        CASE schedule_kind WHEN 'daily' THEN 'skip' ELSE 'run_once_latest' END),
    catch_up_limit = CASE WHEN catch_up_limit < 0 THEN 0 ELSE catch_up_limit END
WHERE command_type IS NULL OR command_digest IS NULL OR missed_run_policy IS NULL;

ALTER TABLE scheduled_transactions
    ADD CONSTRAINT scheduled_transactions_missed_policy_check
        CHECK (missed_run_policy IS NULL OR missed_run_policy IN ('skip','run_once_latest','catch_up_bounded')),
    ADD CONSTRAINT scheduled_transactions_catch_up_limit_check
        CHECK (catch_up_limit BETWEEN 0 AND 7),
    ADD CONSTRAINT scheduled_transactions_fee_cap_check
        CHECK (max_fee_amount IS NULL OR max_fee_amount >= 0),
    ADD CONSTRAINT scheduled_transactions_infrastructure_attempts_check
        CHECK (max_infrastructure_attempts BETWEEN 1 AND 20),
    ADD CONSTRAINT scheduled_transactions_retry_window_check
        CHECK (retry_window_seconds > 0),
    ADD CONSTRAINT scheduled_transactions_fee_mode_check
        CHECK (fee_mode IN ('current_policy_with_consent_cap')),
    ADD CONSTRAINT scheduled_transactions_failure_threshold_check
        CHECK (consecutive_failure_threshold BETWEEN 1 AND 20),
    ADD CONSTRAINT scheduled_transactions_failure_count_check
        CHECK (consecutive_failure_count >= 0),
    ADD CONSTRAINT scheduled_transactions_command_version_check
        CHECK (command_version > 0);

ALTER TABLE scheduled_transactions DROP CONSTRAINT IF EXISTS scheduled_transactions_status_check;
ALTER TABLE scheduled_transactions ADD CONSTRAINT scheduled_transactions_status_check
    CHECK (status IN ('active','paused','finished','failed','blocked'));

CREATE INDEX idx_sched_tx_planner ON scheduled_transactions(status, run_at_date, last_planned_at);

CREATE TABLE scheduled_occurrences (
    id                    UUID PRIMARY KEY,
    public_id             TEXT NOT NULL UNIQUE,
    schedule_id           UUID NOT NULL REFERENCES scheduled_transactions(id),
    schedule_version      BIGINT NOT NULL,
    scheduled_for         TIMESTAMPTZ NOT NULL,
    scheduled_local_date  DATE NOT NULL,
    status                TEXT NOT NULL CHECK (status IN
        ('planned','due','screening','ready','processing','retry_wait','succeeded',
         'failed_business','failed_terminal','blocked','skipped_missed',
         'skipped_superseded','cancelled','expired')),
    idempotency_key       TEXT NOT NULL UNIQUE,
    policy_snapshot       JSONB NOT NULL,
    fee_amount            BIGINT NULL CHECK (fee_amount IS NULL OR fee_amount >= 0),
    fee_quote_id          UUID NULL,
    ledger_transaction_id UUID NULL,
    attempt_count         INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at       TIMESTAMPTZ NULL,
    lease_owner           TEXT NULL,
    lease_expires_at      TIMESTAMPTZ NULL,
    error_code            TEXT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (schedule_id, scheduled_for)
);

CREATE INDEX idx_sched_occurrences_dispatch
    ON scheduled_occurrences(status, next_attempt_at, scheduled_for);
CREATE INDEX idx_sched_occurrences_schedule
    ON scheduled_occurrences(schedule_id, scheduled_for DESC);

CREATE TABLE scheduled_execution_attempts (
    id                    UUID PRIMARY KEY,
    occurrence_id         UUID NOT NULL REFERENCES scheduled_occurrences(id),
    attempt_number        INTEGER NOT NULL CHECK (attempt_number > 0),
    phase                 TEXT NOT NULL,
    result                TEXT NOT NULL,
    retryable             BOOLEAN NOT NULL,
    error_code            TEXT NULL,
    ledger_transaction_id UUID NULL,
    started_at            TIMESTAMPTZ NOT NULL,
    finished_at           TIMESTAMPTZ NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (occurrence_id, attempt_number)
);

CREATE INDEX idx_sched_attempts_occurrence
    ON scheduled_execution_attempts(occurrence_id, attempt_number DESC);

-- C5 system liability accounts.  They are platform-owned, append-only ledger
-- accounts and may temporarily move through zero during close/recovery.
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by)
VALUES
 ('00000000-0000-0000-0000-000000000031','system','accrued_interest_payable','IDR',NULL,'migration'),
 ('00000000-0000-0000-0000-000000000032','system','accrued_interest_payable','USD',NULL,'migration')
ON CONFLICT (id) DO NOTHING;

INSERT INTO account_balances (account_id, allow_negative)
VALUES
('00000000-0000-0000-0000-000000000031', true),
('00000000-0000-0000-0000-000000000032', true)
ON CONFLICT (account_id) DO NOTHING;

-- Financial evidence is append-only at the database boundary.  The service
-- may advance workflow/status and operational lease fields, but it cannot
-- rewrite product terms, rate terms, enrollment identity, snapshots, or a
-- closed period's evidence underneath the maker/checker and close workflows.
CREATE OR REPLACE FUNCTION fn_prevent_c5_snapshot_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM interest_daily_accruals
        WHERE snapshot_id = OLD.id
    ) THEN
        RAISE EXCEPTION 'account balance snapshot referenced by C5 accruals is immutable — use a correcting ledger transaction';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_c5_snapshot_immutable
    BEFORE UPDATE OR DELETE ON account_balance_snapshots
    FOR EACH ROW EXECUTE FUNCTION fn_prevent_c5_snapshot_mutation();

CREATE OR REPLACE FUNCTION fn_guard_c5_product_terms() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.public_id IS DISTINCT FROM OLD.public_id
       OR NEW.product_code IS DISTINCT FROM OLD.product_code
       OR NEW.name IS DISTINCT FROM OLD.name
       OR NEW.currency IS DISTINCT FROM OLD.currency
       OR NEW.eligible_account_types IS DISTINCT FROM OLD.eligible_account_types
       OR NEW.day_count_convention IS DISTINCT FROM OLD.day_count_convention
       OR NEW.capitalization_frequency IS DISTINCT FROM OLD.capitalization_frequency
       OR NEW.timezone IS DISTINCT FROM OLD.timezone
       OR NEW.minimum_eligible_balance IS DISTINCT FROM OLD.minimum_eligible_balance
       OR NEW.interest_expense_account_id IS DISTINCT FROM OLD.interest_expense_account_id
       OR NEW.interest_payable_account_id IS DISTINCT FROM OLD.interest_payable_account_id
       OR NEW.default_rate_policy IS DISTINCT FROM OLD.default_rate_policy
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'savings product terms are immutable — create a new product version';
    END IF;
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_c5_product_terms_immutable
    BEFORE UPDATE ON savings_products
    FOR EACH ROW EXECUTE FUNCTION fn_guard_c5_product_terms();

CREATE OR REPLACE FUNCTION fn_guard_c5_rate_terms() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.public_id IS DISTINCT FROM OLD.public_id
       OR NEW.product_id IS DISTINCT FROM OLD.product_id
       OR NEW.annual_rate_bps IS DISTINCT FROM OLD.annual_rate_bps
       OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
       OR NEW.effective_until IS DISTINCT FROM OLD.effective_until
       OR NEW.content_hash IS DISTINCT FROM OLD.content_hash
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'savings rate terms are immutable — create a new rate version';
    END IF;
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_c5_rate_terms_immutable
    BEFORE UPDATE ON savings_rate_versions
    FOR EACH ROW EXECUTE FUNCTION fn_guard_c5_rate_terms();

CREATE OR REPLACE FUNCTION fn_guard_c5_enrollment_identity() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.public_id IS DISTINCT FROM OLD.public_id
       OR NEW.product_id IS DISTINCT FROM OLD.product_id
       OR NEW.account_id IS DISTINCT FROM OLD.account_id
       OR NEW.user_id IS DISTINCT FROM OLD.user_id
       OR NEW.mode IS DISTINCT FROM OLD.mode
       OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
       OR (NEW.effective_until IS DISTINCT FROM OLD.effective_until AND NOT (
           NEW.status = 'ended'
           AND OLD.status IN ('active','accrual_paused')
           AND NEW.effective_until IS NOT NULL
           AND (OLD.effective_until IS NULL OR NEW.effective_until < OLD.effective_until)
       ))
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'savings enrollment identity is immutable — end and recreate the enrollment';
    END IF;
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_c5_enrollment_identity_immutable
    BEFORE UPDATE ON savings_enrollments
    FOR EACH ROW EXECUTE FUNCTION fn_guard_c5_enrollment_identity();

CREATE OR REPLACE FUNCTION fn_prevent_c5_closed_period_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'closed' THEN
        RAISE EXCEPTION 'closed interest periods are immutable — use a linked correction';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_c5_closed_period_immutable
    BEFORE UPDATE OR DELETE ON interest_periods
    FOR EACH ROW EXECUTE FUNCTION fn_prevent_c5_closed_period_mutation();

CREATE OR REPLACE FUNCTION fn_prevent_c5_closed_period_item_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM interest_periods
        WHERE id=OLD.period_id AND status='closed'
    ) THEN
        RAISE EXCEPTION 'evidence for a closed interest period is immutable — use a linked correction';
    END IF;
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_c5_closed_accrual_immutable
    BEFORE UPDATE OR DELETE ON interest_daily_accruals
    FOR EACH ROW EXECUTE FUNCTION fn_prevent_c5_closed_period_item_mutation();

CREATE TRIGGER trg_c5_closed_capitalization_immutable
    BEFORE UPDATE OR DELETE ON interest_capitalization_items
    FOR EACH ROW EXECUTE FUNCTION fn_prevent_c5_closed_period_item_mutation();

CREATE TRIGGER trg_c5_closed_period_check_immutable
    BEFORE UPDATE OR DELETE ON interest_period_checks
    FOR EACH ROW EXECUTE FUNCTION fn_prevent_c5_closed_period_item_mutation();

CREATE TRIGGER trg_savings_products_ua BEFORE UPDATE ON savings_products
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_savings_enrollments_ua BEFORE UPDATE ON savings_enrollments
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_interest_periods_ua BEFORE UPDATE ON interest_periods
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_interest_daily_accruals_ua BEFORE UPDATE ON interest_daily_accruals
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_interest_capitalization_items_ua BEFORE UPDATE ON interest_capitalization_items
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_scheduled_occurrences_ua BEFORE UPDATE ON scheduled_occurrences
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

GRANT SELECT, INSERT, UPDATE ON savings_products, savings_rate_versions,
    savings_enrollments, savings_enrollment_status_history, interest_periods, interest_daily_accruals,
    interest_capitalization_items, interest_period_checks, interest_adjustments,
    scheduled_occurrences, scheduled_execution_attempts TO app_service;
GRANT SELECT ON savings_products, savings_rate_versions, savings_enrollments,
    savings_enrollment_status_history, interest_periods, interest_daily_accruals, interest_capitalization_items,
    interest_period_checks, interest_adjustments, scheduled_occurrences,
    scheduled_execution_attempts TO app_readonly;

ALTER TABLE savings_products ENABLE ROW LEVEL SECURITY;
ALTER TABLE savings_products FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON savings_products FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON savings_products FOR SELECT TO app_readonly USING (true);
ALTER TABLE savings_rate_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE savings_rate_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON savings_rate_versions FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON savings_rate_versions FOR SELECT TO app_readonly USING (true);
ALTER TABLE savings_enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE savings_enrollments FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON savings_enrollments FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON savings_enrollments FOR SELECT TO app_readonly USING (true);
ALTER TABLE savings_enrollment_status_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE savings_enrollment_status_history FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON savings_enrollment_status_history FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON savings_enrollment_status_history FOR SELECT TO app_readonly USING (true);
ALTER TABLE interest_periods ENABLE ROW LEVEL SECURITY;
ALTER TABLE interest_periods FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON interest_periods FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON interest_periods FOR SELECT TO app_readonly USING (true);
ALTER TABLE interest_daily_accruals ENABLE ROW LEVEL SECURITY;
ALTER TABLE interest_daily_accruals FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON interest_daily_accruals FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON interest_daily_accruals FOR SELECT TO app_readonly USING (true);
ALTER TABLE interest_capitalization_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE interest_capitalization_items FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON interest_capitalization_items FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON interest_capitalization_items FOR SELECT TO app_readonly USING (true);
ALTER TABLE interest_period_checks ENABLE ROW LEVEL SECURITY;
ALTER TABLE interest_period_checks FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON interest_period_checks FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON interest_period_checks FOR SELECT TO app_readonly USING (true);
ALTER TABLE interest_adjustments ENABLE ROW LEVEL SECURITY;
ALTER TABLE interest_adjustments FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON interest_adjustments FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON interest_adjustments FOR SELECT TO app_readonly USING (true);
ALTER TABLE scheduled_occurrences ENABLE ROW LEVEL SECURITY;
ALTER TABLE scheduled_occurrences FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON scheduled_occurrences FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON scheduled_occurrences FOR SELECT TO app_readonly USING (true);
ALTER TABLE scheduled_execution_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE scheduled_execution_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON scheduled_execution_attempts FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON scheduled_execution_attempts FOR SELECT TO app_readonly USING (true);
