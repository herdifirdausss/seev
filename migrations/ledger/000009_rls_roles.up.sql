-- docs/roadmap/archive/16 Task T3 (K9): RLS + minimal grants as defense-in-depth.
-- Ports docs/design/legacy-schemas/ledgernew.sql:648-760 to the canonical
-- schema + every table added since (outbox_events, pending_adjustments,
-- recon_batches, recon_items, account_balance_snapshots).
--
-- This does NOT create a login role or set a password — that's a
-- deployment-time step (see README's deployment section): create a LOGIN
-- role (e.g. seev_app), GRANT app_service TO it, and set POSTGRES_USER to
-- that role. Migrations themselves keep running as the schema owner (DDL
-- identity stays separate from the app's DML identity).

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_service') THEN
        CREATE ROLE app_service;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_readonly') THEN
        CREATE ROLE app_readonly;
    END IF;
END;
$$;

-- ── app_service: per-table grants, minimal per how the Go code actually
-- writes each table (audited against internal/ledger/repository/*.go and
-- internal/ledger/service/*/*.go — zero DELETE statements anywhere in this
-- codebase, so no table gets DELETE). ledger_entries and
-- account_balance_snapshots are INSERT-only for the app even though the DB
-- trigger (trg_entries_immutable) already rejects UPDATE/DELETE on
-- ledger_entries — this grant is the second, independent layer under that
-- trigger, not a replacement for it.
GRANT SELECT, INSERT, UPDATE ON
    accounts, account_balances, ledger_transactions,
    outbox_events, pending_adjustments, recon_batches, recon_items
TO app_service;

GRANT SELECT, INSERT ON
    ledger_entries, account_balance_snapshots
TO app_service;

GRANT SELECT ON v_account_balance_audit TO app_service;

-- ── app_readonly: SELECT everything EXCEPT outbox_events (internal AMQP
-- payload) and pending_adjustments (cmd_payload carries an internal command
-- shape, not meant for reporting consumption).
GRANT SELECT ON
    accounts, account_balances, ledger_transactions, ledger_entries,
    recon_batches, recon_items, account_balance_snapshots,
    v_account_balance_audit
TO app_readonly;

-- ── ENABLE + FORCE RLS on every table (K9: value is grant-minimal + FORCE,
-- not per-tenant policy — see decision rationale in docs/roadmap/archive/13 K9).
ALTER TABLE accounts                    ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_balances            ENABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_transactions         ENABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_entries              ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events               ENABLE ROW LEVEL SECURITY;
ALTER TABLE pending_adjustments         ENABLE ROW LEVEL SECURITY;
ALTER TABLE recon_batches               ENABLE ROW LEVEL SECURITY;
ALTER TABLE recon_items                 ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_balance_snapshots   ENABLE ROW LEVEL SECURITY;

ALTER TABLE accounts                    FORCE ROW LEVEL SECURITY;
ALTER TABLE account_balances            FORCE ROW LEVEL SECURITY;
ALTER TABLE ledger_transactions         FORCE ROW LEVEL SECURITY;
ALTER TABLE ledger_entries              FORCE ROW LEVEL SECURITY;
ALTER TABLE outbox_events               FORCE ROW LEVEL SECURITY;
ALTER TABLE pending_adjustments         FORCE ROW LEVEL SECURITY;
ALTER TABLE recon_batches               FORCE ROW LEVEL SECURITY;
ALTER TABLE recon_items                 FORCE ROW LEVEL SECURITY;
ALTER TABLE account_balance_snapshots   FORCE ROW LEVEL SECURITY;

-- app_service: full access to every row on every table (policy exists on
-- ALL nine tables regardless of which ones it has table-level grants for —
-- FORCE means even the schema owner needs a policy to see rows, and
-- app_service is meant to see everything it's granted).
CREATE POLICY pol_all_service ON accounts                  FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON account_balances          FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON ledger_transactions       FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON ledger_entries             FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON outbox_events              FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON pending_adjustments         FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON recon_batches               FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON recon_items                 FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service ON account_balance_snapshots   FOR ALL TO app_service USING (true) WITH CHECK (true);

-- app_readonly: SELECT only, only on the tables it has a table-level grant
-- for — outbox_events/pending_adjustments get no policy for this role
-- (already blocked at the grant level; RLS+FORCE is defense in depth in
-- case a future grant is added carelessly).
CREATE POLICY pol_read_readonly ON accounts                  FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON account_balances          FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON ledger_transactions       FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON ledger_entries             FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON recon_batches               FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON recon_items                 FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_read_readonly ON account_balance_snapshots   FOR SELECT TO app_readonly USING (true);
