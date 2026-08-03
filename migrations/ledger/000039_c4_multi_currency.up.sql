-- C4 end-to-end multi-currency activation.
--
-- Currency remains an explicit ledger boundary: every transaction and every
-- entry has exactly one currency. FX is represented by two balanced ledger
-- transactions linked to one conversion, and both legs are committed by the
-- same database transaction.

-- Scheduled/disbursement intake must preserve the requested currency at its
-- durable boundary. Existing manifests remain IDR-compatible through the
-- default; new rows may explicitly target any registered currency.
ALTER TABLE disbursement_items
    ADD COLUMN currency CHAR(3) NOT NULL DEFAULT 'IDR'
        REFERENCES currencies(code),
    ADD CONSTRAINT disbursement_items_currency_shape
        CHECK (currency ~ '^[A-Z]{3}$');

ALTER TABLE currencies
    ADD COLUMN status TEXT,
    ADD COLUMN operations JSONB;

UPDATE currencies
SET status = CASE WHEN enabled THEN 'active' ELSE 'disabled' END,
    operations = '{"account_enable":true,"topup":true,"transfer":true,"payout":true,"fx_source":true,"fx_target":true,"statement":true,"notification_display":true}'::jsonb;

ALTER TABLE currencies
    ALTER COLUMN status SET DEFAULT 'active',
    ALTER COLUMN status SET NOT NULL,
    ALTER COLUMN operations SET DEFAULT '{"account_enable":true,"topup":true,"transfer":true,"payout":true,"fx_source":true,"fx_target":true,"statement":true,"notification_display":true}'::jsonb,
    ALTER COLUMN operations SET NOT NULL,
    ADD CONSTRAINT currencies_status_check
        CHECK (status IN ('draft', 'active', 'intake_paused', 'disabled'));

CREATE TABLE fx_pairs (
    id                   UUID PRIMARY KEY,
    pair_code            TEXT NOT NULL UNIQUE,
    base_currency        CHAR(3) NOT NULL REFERENCES currencies(code),
    quote_currency       CHAR(3) NOT NULL REFERENCES currencies(code),
    status               TEXT NOT NULL DEFAULT 'active'
                         CHECK (status IN ('draft', 'active', 'paused', 'disabled')),
    rate_source          TEXT NOT NULL,
    rate_convention      TEXT NOT NULL DEFAULT 'IDR per USD',
    position_qualifier   TEXT NOT NULL,
    pair_policy_version  BIGINT NOT NULL DEFAULT 1 CHECK (pair_policy_version > 0),
    quote_ttl_seconds    INT NOT NULL DEFAULT 30 CHECK (quote_ttl_seconds BETWEEN 1 AND 86400),
    rounding_mode        TEXT NOT NULL DEFAULT 'toward_zero'
                         CHECK (rounding_mode IN ('toward_zero')),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fx_pairs_distinct_currencies CHECK (base_currency <> quote_currency),
    UNIQUE (base_currency, quote_currency),
    UNIQUE (position_qualifier)
);

CREATE TABLE fx_pair_directions (
    id                    UUID PRIMARY KEY,
    pair_id               UUID NOT NULL REFERENCES fx_pairs(id),
    source_currency       CHAR(3) NOT NULL REFERENCES currencies(code),
    target_currency       CHAR(3) NOT NULL REFERENCES currencies(code),
    enabled               BOOLEAN NOT NULL DEFAULT true,
    new_quotes_paused     BOOLEAN NOT NULL DEFAULT false,
    conversions_paused    BOOLEAN NOT NULL DEFAULT false,
    min_source_amount     BIGINT NOT NULL DEFAULT 1 CHECK (min_source_amount > 0),
    max_source_amount     BIGINT NOT NULL CHECK (max_source_amount > min_source_amount),
    spread_basis_points   BIGINT NOT NULL DEFAULT 0 CHECK (spread_basis_points BETWEEN 0 AND 9999),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fx_pair_directions_distinct_currencies CHECK (source_currency <> target_currency),
    UNIQUE (pair_id, source_currency, target_currency)
);

CREATE OR REPLACE FUNCTION fn_guard_fx_direction_pair() RETURNS TRIGGER AS $$
DECLARE
    pair_base CHAR(3);
    pair_quote CHAR(3);
BEGIN
    SELECT base_currency, quote_currency
      INTO pair_base, pair_quote
      FROM fx_pairs
     WHERE id = NEW.pair_id;
    IF pair_base IS NULL OR NOT (
        (NEW.source_currency = pair_base AND NEW.target_currency = pair_quote)
        OR (NEW.source_currency = pair_quote AND NEW.target_currency = pair_base)
    ) THEN
        RAISE EXCEPTION 'FX direction currencies must be the pair currencies';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_fx_direction_pair_guard
    BEFORE INSERT OR UPDATE OF pair_id, source_currency, target_currency
    ON fx_pair_directions
    FOR EACH ROW EXECUTE FUNCTION fn_guard_fx_direction_pair();

CREATE TABLE fx_rate_versions (
    id                 UUID PRIMARY KEY,
    pair_id            UUID NOT NULL REFERENCES fx_pairs(id),
    direction_id       UUID NOT NULL REFERENCES fx_pair_directions(id),
    version            BIGINT NOT NULL CHECK (version > 0),
    reference_rate     NUMERIC(38,18) NOT NULL CHECK (reference_rate > 0),
    rate_source        TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('draft', 'pending_approval', 'active', 'expired', 'retired', 'rejected')),
    effective_from     TIMESTAMPTZ NOT NULL,
    effective_to       TIMESTAMPTZ NULL,
    created_by         TEXT NOT NULL DEFAULT 'system',
    submitted_by       TEXT NULL,
    approved_by        TEXT NULL,
    submitted_at       TIMESTAMPTZ NULL,
    approved_at        TIMESTAMPTZ NULL,
    retired_at         TIMESTAMPTZ NULL,
    rejection_reason   TEXT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fx_rate_versions_effective_window
        CHECK (effective_to IS NULL OR effective_to > effective_from),
    UNIQUE (direction_id, version)
);

CREATE OR REPLACE FUNCTION fn_guard_fx_rate_overlap() RETURNS TRIGGER AS $$
BEGIN
    -- Service approvals lock the direction row as well; this advisory lock
    -- keeps the invariant intact for direct SQL/admin jobs that bypass the
    -- Go workflow and would otherwise race between the overlap check and
    -- INSERT/UPDATE.
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.direction_id::text, 0));
    IF NEW.status = 'active' AND EXISTS (
        SELECT 1
        FROM fx_rate_versions current_rate
        WHERE current_rate.direction_id = NEW.direction_id
          AND current_rate.status = 'active'
          AND current_rate.id <> NEW.id
          AND current_rate.effective_from < COALESCE(NEW.effective_to, 'infinity'::timestamptz)
          AND (current_rate.effective_to IS NULL OR current_rate.effective_to > NEW.effective_from)
    ) THEN
        RAISE EXCEPTION 'overlapping active FX rate window for direction %', NEW.direction_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_fx_rate_overlap_guard
    BEFORE INSERT OR UPDATE OF status, effective_from, effective_to, direction_id
    ON fx_rate_versions
    FOR EACH ROW EXECUTE FUNCTION fn_guard_fx_rate_overlap();

CREATE OR REPLACE FUNCTION fn_guard_fx_rate_pair() RETURNS TRIGGER AS $$
DECLARE
    direction_pair UUID;
BEGIN
    SELECT pair_id INTO direction_pair
      FROM fx_pair_directions
     WHERE id = NEW.direction_id;
    IF direction_pair IS NULL OR direction_pair <> NEW.pair_id THEN
        RAISE EXCEPTION 'FX rate pair and direction do not match';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_fx_rate_pair_guard
    BEFORE INSERT OR UPDATE OF pair_id, direction_id
    ON fx_rate_versions
    FOR EACH ROW EXECUTE FUNCTION fn_guard_fx_rate_pair();

CREATE OR REPLACE FUNCTION fn_guard_fx_rate_immutable() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.pair_id IS DISTINCT FROM OLD.pair_id
       OR NEW.direction_id IS DISTINCT FROM OLD.direction_id
       OR NEW.version IS DISTINCT FROM OLD.version
       OR NEW.reference_rate IS DISTINCT FROM OLD.reference_rate
       OR NEW.rate_source IS DISTINCT FROM OLD.rate_source
       OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
       OR NEW.effective_to IS DISTINCT FROM OLD.effective_to
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'FX rate version payload is immutable; create a new version';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_fx_rate_immutable_guard
    BEFORE UPDATE ON fx_rate_versions
    FOR EACH ROW EXECUTE FUNCTION fn_guard_fx_rate_immutable();

CREATE TABLE fx_quotes (
    id                         UUID PRIMARY KEY,
    user_id                    UUID NOT NULL,
    pair_id                    UUID NOT NULL REFERENCES fx_pairs(id),
    direction_id               UUID NOT NULL REFERENCES fx_pair_directions(id),
    rate_version_id            UUID NOT NULL REFERENCES fx_rate_versions(id),
    source_currency            CHAR(3) NOT NULL REFERENCES currencies(code),
    target_currency            CHAR(3) NOT NULL REFERENCES currencies(code),
    source_minor_unit           SMALLINT NOT NULL CHECK (source_minor_unit >= 0),
    target_minor_unit           SMALLINT NOT NULL CHECK (target_minor_unit >= 0),
    source_amount              BIGINT NOT NULL CHECK (source_amount > 0),
    target_amount              BIGINT NOT NULL CHECK (target_amount > 0),
    reference_rate              NUMERIC(38,18) NOT NULL CHECK (reference_rate > 0),
    -- A reverse quote with a non-zero spread can be a non-terminating
    -- decimal. Store the exact decimal/rational wire form instead of
    -- allowing NUMERIC to round it before conversion evidence is emitted.
    client_rate                 TEXT NOT NULL CHECK (
        (client_rate ~ '^[0-9]+([.][0-9]+)?$'
         OR client_rate ~ '^[1-9][0-9]*/[1-9][0-9]*$')
        AND client_rate !~ '^0+([.]0+)?$'
    ),
    rate_convention             TEXT NOT NULL,
    pair_policy_version         BIGINT NOT NULL CHECK (pair_policy_version > 0),
    spread_basis_points         BIGINT NOT NULL CHECK (spread_basis_points BETWEEN 0 AND 9999),
    rounding_mode               TEXT NOT NULL CHECK (rounding_mode IN ('toward_zero')),
    rounding_remainder_numerator NUMERIC(120,0) NOT NULL DEFAULT 0,
    rounding_remainder_denominator NUMERIC(120,0) NOT NULL DEFAULT 1
                               CHECK (rounding_remainder_denominator > 0),
    request_key                 TEXT NOT NULL,
    status                      TEXT NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'consumed', 'expired', 'cancelled')),
    expires_at                  TIMESTAMPTZ NOT NULL,
    consumed_at                 TIMESTAMPTZ NULL,
    consumed_by_conversion_id   UUID NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (user_id, request_key),
    CONSTRAINT fx_quotes_distinct_currencies CHECK (source_currency <> target_currency)
);

CREATE INDEX idx_fx_quotes_user_status ON fx_quotes(user_id, status, created_at DESC);
CREATE INDEX idx_fx_quotes_expiry ON fx_quotes(expires_at) WHERE status = 'active';

CREATE OR REPLACE FUNCTION fn_guard_fx_quote_consistency() RETURNS TRIGGER AS $$
DECLARE
    direction_pair UUID;
    direction_source CHAR(3);
    direction_target CHAR(3);
    rate_pair UUID;
    rate_direction UUID;
BEGIN
    SELECT pair_id, source_currency, target_currency
      INTO direction_pair, direction_source, direction_target
      FROM fx_pair_directions
     WHERE id = NEW.direction_id;
    SELECT pair_id, direction_id
      INTO rate_pair, rate_direction
      FROM fx_rate_versions
     WHERE id = NEW.rate_version_id;
    IF direction_pair IS NULL OR direction_pair <> NEW.pair_id
       OR direction_source <> NEW.source_currency
       OR direction_target <> NEW.target_currency
       OR rate_pair IS NULL OR rate_pair <> NEW.pair_id
       OR rate_direction <> NEW.direction_id THEN
        RAISE EXCEPTION 'FX quote pair, direction, rate, and currencies must match';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_fx_quote_consistency_guard
    BEFORE INSERT OR UPDATE OF pair_id, direction_id, rate_version_id,
        source_currency, target_currency
    ON fx_quotes
    FOR EACH ROW EXECUTE FUNCTION fn_guard_fx_quote_consistency();

CREATE TABLE fx_conversions (
    id                     UUID PRIMARY KEY,
    user_id                UUID NOT NULL,
    quote_id               UUID NOT NULL REFERENCES fx_quotes(id),
    idempotency_key        TEXT NOT NULL,
    source_currency        CHAR(3) NOT NULL REFERENCES currencies(code),
    target_currency        CHAR(3) NOT NULL REFERENCES currencies(code),
    source_amount          BIGINT NOT NULL CHECK (source_amount > 0),
    target_amount          BIGINT NOT NULL CHECK (target_amount > 0),
    status                 TEXT NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending', 'posted', 'failed')),
    source_transaction_id  UUID NULL REFERENCES ledger_transactions(id),
    target_transaction_id  UUID NULL REFERENCES ledger_transactions(id),
    error_message          TEXT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    posted_at              TIMESTAMPTZ NULL,

    UNIQUE (user_id, idempotency_key),
    UNIQUE (quote_id)
);

CREATE TABLE fx_position_limits (
	    pair_id                    UUID NOT NULL REFERENCES fx_pairs(id),
    currency                   CHAR(3) NOT NULL REFERENCES currencies(code),
	    minimum_balance            BIGINT NOT NULL,
	    maximum_balance            BIGINT NOT NULL,
	    warning_minimum_balance    BIGINT NULL,
	    warning_maximum_balance    BIGINT NULL,
	    critical_minimum_balance   BIGINT NULL,
	    critical_maximum_balance   BIGINT NULL,
	    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
	    PRIMARY KEY (pair_id, currency),
	    CHECK (maximum_balance > minimum_balance),
	CHECK (warning_minimum_balance IS NULL OR warning_minimum_balance >= minimum_balance),
	CHECK (warning_maximum_balance IS NULL OR warning_maximum_balance <= maximum_balance),
	CHECK (critical_minimum_balance IS NULL OR critical_minimum_balance >= minimum_balance),
	CHECK (critical_maximum_balance IS NULL OR critical_maximum_balance <= maximum_balance),
	CHECK (warning_minimum_balance IS NULL OR warning_maximum_balance IS NULL OR warning_minimum_balance <= warning_maximum_balance),
	CHECK (critical_minimum_balance IS NULL OR critical_maximum_balance IS NULL OR critical_minimum_balance <= critical_maximum_balance)
);

ALTER TABLE ledger_transactions
    ADD COLUMN conversion_id UUID NULL REFERENCES fx_conversions(id),
    ADD COLUMN fx_quote_id UUID NULL REFERENCES fx_quotes(id),
    ADD COLUMN fx_leg TEXT NULL CHECK (fx_leg IN ('source', 'target')),
    ADD COLUMN counterpart_transaction_id UUID NULL REFERENCES ledger_transactions(id),
    ADD CONSTRAINT ledger_transactions_fx_shape CHECK (
        (conversion_id IS NULL AND fx_quote_id IS NULL AND fx_leg IS NULL AND counterpart_transaction_id IS NULL)
        OR (conversion_id IS NOT NULL AND fx_quote_id IS NOT NULL AND fx_leg IS NOT NULL AND counterpart_transaction_id IS NOT NULL)
    );

CREATE INDEX idx_ltx_conversion ON ledger_transactions(conversion_id) WHERE conversion_id IS NOT NULL;
CREATE INDEX idx_ltx_fx_quote ON ledger_transactions(fx_quote_id) WHERE fx_quote_id IS NOT NULL;
CREATE UNIQUE INDEX idx_ltx_conversion_leg ON ledger_transactions(conversion_id, fx_leg)
    WHERE conversion_id IS NOT NULL;
CREATE INDEX idx_fx_conversions_user ON fx_conversions(user_id, created_at DESC);

CREATE OR REPLACE FUNCTION fn_guard_fx_conversion_consistency() RETURNS TRIGGER AS $$
DECLARE
    quote_user UUID;
    quote_source CHAR(3);
    quote_target CHAR(3);
    quote_source_amount BIGINT;
    quote_target_amount BIGINT;
BEGIN
    SELECT user_id, source_currency, target_currency, source_amount, target_amount
      INTO quote_user, quote_source, quote_target, quote_source_amount, quote_target_amount
      FROM fx_quotes
     WHERE id = NEW.quote_id;
    IF quote_user IS NULL OR quote_user <> NEW.user_id
       OR quote_source <> NEW.source_currency
       OR quote_target <> NEW.target_currency
       OR quote_source_amount <> NEW.source_amount
       OR quote_target_amount <> NEW.target_amount THEN
        RAISE EXCEPTION 'FX conversion must match its quote and user';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_fx_conversion_consistency_guard
    BEFORE INSERT OR UPDATE OF quote_id, user_id, source_currency, target_currency,
        source_amount, target_amount
    ON fx_conversions
    FOR EACH ROW EXECUTE FUNCTION fn_guard_fx_conversion_consistency();

ALTER TABLE fx_pairs ENABLE ROW LEVEL SECURITY;
ALTER TABLE fx_pair_directions ENABLE ROW LEVEL SECURITY;
ALTER TABLE fx_rate_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE fx_quotes ENABLE ROW LEVEL SECURITY;
ALTER TABLE fx_conversions ENABLE ROW LEVEL SECURITY;
ALTER TABLE fx_position_limits ENABLE ROW LEVEL SECURITY;

ALTER TABLE fx_pairs FORCE ROW LEVEL SECURITY;
ALTER TABLE fx_pair_directions FORCE ROW LEVEL SECURITY;
ALTER TABLE fx_rate_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE fx_quotes FORCE ROW LEVEL SECURITY;
ALTER TABLE fx_conversions FORCE ROW LEVEL SECURITY;
ALTER TABLE fx_position_limits FORCE ROW LEVEL SECURITY;

GRANT SELECT, INSERT, UPDATE ON
    fx_pairs, fx_pair_directions, fx_rate_versions, fx_quotes,
    fx_conversions, fx_position_limits TO app_service;
GRANT SELECT ON
    fx_pairs, fx_pair_directions, fx_rate_versions, fx_quotes,
    fx_conversions, fx_position_limits TO app_readonly;

CREATE POLICY pol_all_service ON fx_pairs FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON fx_pair_directions FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON fx_rate_versions FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON fx_quotes FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON fx_conversions FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON fx_position_limits FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON fx_pairs FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON fx_pair_directions FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON fx_rate_versions FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON fx_quotes FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON fx_conversions FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON fx_position_limits FOR SELECT TO app_readonly USING (true);

CREATE OR REPLACE FUNCTION fn_guard_ledger_transaction_currency() RETURNS TRIGGER AS $$
DECLARE
    source_currency CHAR(3);
    destination_currency CHAR(3);
BEGIN
    IF NEW.source_account_id IS NOT NULL THEN
        SELECT currency INTO source_currency FROM accounts WHERE id = NEW.source_account_id;
        IF source_currency IS NULL OR source_currency <> NEW.currency THEN
            RAISE EXCEPTION 'source account currency must match transaction currency';
        END IF;
    END IF;
    IF NEW.destination_account_id IS NOT NULL THEN
        SELECT currency INTO destination_currency FROM accounts WHERE id = NEW.destination_account_id;
        IF destination_currency IS NULL OR destination_currency <> NEW.currency THEN
            RAISE EXCEPTION 'destination account currency must match transaction currency';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ltx_currency_guard
    BEFORE INSERT OR UPDATE OF currency, source_account_id, destination_account_id
    ON ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION fn_guard_ledger_transaction_currency();

CREATE OR REPLACE FUNCTION fn_guard_ledger_entry_currency() RETURNS TRIGGER AS $$
DECLARE
    transaction_currency CHAR(3);
    account_currency CHAR(3);
BEGIN
    SELECT currency INTO transaction_currency FROM ledger_transactions WHERE id = NEW.transaction_id;
    SELECT currency INTO account_currency FROM accounts WHERE id = NEW.account_id;
    IF transaction_currency IS NULL OR account_currency IS NULL OR transaction_currency <> account_currency THEN
        RAISE EXCEPTION 'ledger entry account currency must match transaction currency';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_entries_currency_guard
    BEFORE INSERT ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION fn_guard_ledger_entry_currency();

CREATE TRIGGER trg_fx_pairs_ua BEFORE UPDATE ON fx_pairs
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_fx_pair_directions_ua BEFORE UPDATE ON fx_pair_directions
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_fx_position_limits_ua BEFORE UPDATE ON fx_position_limits
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

INSERT INTO fx_pairs (
    id, pair_code, base_currency, quote_currency, status, rate_source,
    rate_convention, position_qualifier, pair_policy_version, quote_ttl_seconds, rounding_mode
) VALUES (
    '00000000-0000-7000-8000-000000000039', 'USDIDR', 'USD', 'IDR', 'active', 'mock',
    'IDR per USD', 'IDRUSD', 1, 30, 'toward_zero'
);

INSERT INTO fx_pair_directions (
    id, pair_id, source_currency, target_currency, min_source_amount,
    max_source_amount, spread_basis_points
) VALUES
(
    '00000000-0000-7000-8000-000000000040',
    '00000000-0000-7000-8000-000000000039', 'IDR', 'USD', 1,
    9000000000000000000, 0
),
(
    '00000000-0000-7000-8000-000000000041',
    '00000000-0000-7000-8000-000000000039', 'USD', 'IDR', 1,
    9000000000000000000, 0
);

INSERT INTO fx_rate_versions (
    id, pair_id, direction_id, version, reference_rate, rate_source,
    status, effective_from, approved_by
) VALUES
(
    '00000000-0000-7000-8000-000000000042',
    '00000000-0000-7000-8000-000000000039',
    '00000000-0000-7000-8000-000000000040', 1, 16000, 'mock',
    'active', '1970-01-01 00:00:00+00', 'migration'
),
(
    '00000000-0000-7000-8000-000000000043',
    '00000000-0000-7000-8000-000000000039',
    '00000000-0000-7000-8000-000000000041', 1, 16000, 'mock',
    'active', '1970-01-01 00:00:00+00', 'migration'
);

INSERT INTO fx_position_limits (pair_id, currency, minimum_balance, maximum_balance)
VALUES
('00000000-0000-7000-8000-000000000039', 'IDR', -9000000000000000000, 9000000000000000000),
('00000000-0000-7000-8000-000000000039', 'USD', -9000000000000000000, 9000000000000000000);

UPDATE fx_position_limits
SET warning_minimum_balance = -7200000000000000000,
    warning_maximum_balance = 7200000000000000000,
    critical_minimum_balance = -8100000000000000000,
    critical_maximum_balance = 8100000000000000000;
