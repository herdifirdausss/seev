-- A11: ledger_entries may only be written through this narrow posting
-- boundary. The runtime role retains SELECT for statements/reporting, but no
-- table-level write privilege. A dedicated NOLOGIN owner keeps SECURITY
-- DEFINER authority separate from both the migration login and app_service.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'ledger_posting_owner') THEN
        CREATE ROLE ledger_posting_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
    END IF;
END;
$$;

GRANT USAGE ON SCHEMA public TO ledger_posting_owner;
GRANT SELECT, INSERT ON public.ledger_entries TO ledger_posting_owner;
GRANT SELECT ON public.accounts, public.ledger_transactions TO ledger_posting_owner;

REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON public.ledger_entries FROM app_service;

DROP POLICY IF EXISTS pol_all_service ON public.ledger_entries;
DROP POLICY IF EXISTS pol_ledger_entries_read_service ON public.ledger_entries;
DROP POLICY IF EXISTS pol_ledger_entries_posting_owner ON public.ledger_entries;
CREATE POLICY pol_ledger_entries_read_service
    ON public.ledger_entries FOR SELECT TO app_service USING (true);
CREATE POLICY pol_ledger_entries_posting_owner
    ON public.ledger_entries FOR ALL TO ledger_posting_owner
    USING (true) WITH CHECK (true);
-- The existing currency trigger reads these two RLS-protected tables while
-- the SECURITY DEFINER owner is current_user.
CREATE POLICY pol_accounts_posting_owner
    ON public.accounts FOR SELECT TO ledger_posting_owner USING (true);
CREATE POLICY pol_ledger_transactions_posting_owner
    ON public.ledger_transactions FOR SELECT TO ledger_posting_owner USING (true);

CREATE FUNCTION public.fn_post_ledger_entries_bulk(
    p_transaction_id UUID,
    p_entries JSONB
) RETURNS INTEGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_entry       JSONB;
    v_count       INTEGER;
    v_debit       BIGINT := 0;
    v_credit      BIGINT := 0;
    v_amount      BIGINT;
    v_direction   TEXT;
BEGIN
    IF p_transaction_id IS NULL THEN
        RAISE EXCEPTION 'ledger posting: transaction_id is required'
            USING ERRCODE = '22023';
    END IF;
    IF p_entries IS NULL OR jsonb_typeof(p_entries) <> 'array' THEN
        RAISE EXCEPTION 'ledger posting: entries must be a JSON array'
            USING ERRCODE = '22023';
    END IF;

    -- A ledger transaction is posted exactly once. Serialize callers by
    -- transaction id before checking existing entries so two concurrent
    -- function calls cannot both observe an empty posting and append separate
    -- balanced batches. The application normally holds stronger account
    -- locks already; this lock also protects the database boundary when the
    -- function is called directly through the app role.
    PERFORM pg_catalog.pg_advisory_xact_lock(
        pg_catalog.hashtextextended(p_transaction_id::TEXT, 0)
    );
    IF EXISTS (
        SELECT 1
        FROM public.ledger_entries
        WHERE transaction_id = p_transaction_id
    ) THEN
        RAISE EXCEPTION 'ledger posting: transaction % already has ledger entries',
            p_transaction_id
            USING ERRCODE = '23505';
    END IF;

    v_count := jsonb_array_length(p_entries);
    IF v_count < 1 OR v_count > 50 THEN
        RAISE EXCEPTION 'ledger posting: entries count % is outside [1,50]', v_count
            USING ERRCODE = '22023';
    END IF;

    FOR v_entry IN SELECT value FROM jsonb_array_elements(p_entries)
    LOOP
        IF jsonb_typeof(v_entry) <> 'object'
           OR NOT (v_entry ? 'id')
           OR NOT (v_entry ? 'account_id')
           OR NOT (v_entry ? 'direction')
           OR NOT (v_entry ? 'amount')
           OR NOT (v_entry ? 'balance_after') THEN
            RAISE EXCEPTION 'ledger posting: every entry requires id, account_id, direction, amount, and balance_after'
                USING ERRCODE = '22023';
        END IF;

        -- Casts are intentional validation: malformed UUID/integer values fail
        -- before any row is inserted by the statement below.
        PERFORM (v_entry->>'id')::UUID, (v_entry->>'account_id')::UUID,
                (v_entry->>'balance_after')::BIGINT;
        v_amount := (v_entry->>'amount')::BIGINT;
        v_direction := v_entry->>'direction';
        IF v_amount <= 0 THEN
            RAISE EXCEPTION 'ledger posting: amount must be positive'
                USING ERRCODE = '23514';
        END IF;
        IF v_direction = 'debit' THEN
            v_debit := v_debit + v_amount;
        ELSIF v_direction = 'credit' THEN
            v_credit := v_credit + v_amount;
        ELSE
            RAISE EXCEPTION 'ledger posting: direction must be debit or credit'
                USING ERRCODE = '23514';
        END IF;
    END LOOP;

    IF v_debit IS DISTINCT FROM v_credit THEN
        RAISE EXCEPTION 'ledger posting: transaction % is not balanced (debit %, credit %)',
            p_transaction_id, v_debit, v_credit
            USING ERRCODE = '23514';
    END IF;

    INSERT INTO public.ledger_entries
        (id, transaction_id, account_id, direction, amount, balance_after, note, created_at)
    SELECT (entry->>'id')::UUID,
           p_transaction_id,
           (entry->>'account_id')::UUID,
           entry->>'direction',
           (entry->>'amount')::BIGINT,
           (entry->>'balance_after')::BIGINT,
           NULLIF(entry->>'note', ''),
           statement_timestamp()
    FROM jsonb_array_elements(p_entries) AS item(entry);

    RETURN v_count;
END;
$$;

ALTER FUNCTION public.fn_post_ledger_entries_bulk(UUID, JSONB) OWNER TO ledger_posting_owner;
REVOKE ALL ON FUNCTION public.fn_post_ledger_entries_bulk(UUID, JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.fn_post_ledger_entries_bulk(UUID, JSONB) TO app_service;
