-- Safe only before an application release depends on fn_post_ledger_entries_bulk.
REVOKE EXECUTE ON FUNCTION public.fn_post_ledger_entries_bulk(UUID, JSONB) FROM app_service;
DROP FUNCTION IF EXISTS public.fn_post_ledger_entries_bulk(UUID, JSONB);

DROP POLICY IF EXISTS pol_ledger_entries_posting_owner ON public.ledger_entries;
DROP POLICY IF EXISTS pol_ledger_entries_read_service ON public.ledger_entries;
DROP POLICY IF EXISTS pol_accounts_posting_owner ON public.accounts;
DROP POLICY IF EXISTS pol_ledger_transactions_posting_owner ON public.ledger_transactions;
CREATE POLICY pol_all_service ON public.ledger_entries
    FOR ALL TO app_service USING (true) WITH CHECK (true);
GRANT SELECT, INSERT ON public.ledger_entries TO app_service;

REVOKE SELECT, INSERT ON public.ledger_entries FROM ledger_posting_owner;
REVOKE SELECT ON public.accounts, public.ledger_transactions FROM ledger_posting_owner;
REVOKE USAGE ON SCHEMA public FROM ledger_posting_owner;
DROP ROLE IF EXISTS ledger_posting_owner;
