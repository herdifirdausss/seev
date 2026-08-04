-- recon_items.amount was unconstrained despite every insert path already
-- enforcing positivity at the application layer: services/ledger/internal/ledger/recon.
-- Service.ImportBatch rejects any CSV row where !r.Amount.IsPositive()
-- before it ever reaches recon_items, and RunMatcher's "missing_external"
-- path copies ledger_transactions.amount, itself guarded CHECK (amount > 0)
-- (services/ledger/migrations/000001_ledger_core.up.sql). This is a DB-level
-- backstop matching the invariant the application already guarantees, the
-- same reasoning already applied to every other amount column in this
-- schema (found during a broader schema audit).
ALTER TABLE recon_items ADD CONSTRAINT chk_recon_items_amount_positive CHECK (amount > 0);
