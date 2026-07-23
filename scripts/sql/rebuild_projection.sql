-- Rebuild account_balances.balance from ledger_entries — the single source
-- of truth (docs/roadmap/archive/17 Task T2, docs/roadmap/archive/01 decision: entries are the
-- ledger, account_balances is a cache of them).
--
-- Deliberately UPDATE, never TRUNCATE + re-insert: allow_negative is NOT
-- derived from entries (seeded per account type in migrations/000002 and
-- 000008, can diverge from any type-based rule in the future) — truncating
-- the table would destroy it. This file is read verbatim by both
-- scripts/rebuild-projection.sh and the Go integration test
-- (TestSchemaContract_RebuildProjection) so there is exactly one copy of
-- this SQL to keep correct.
--
-- Idempotent: running it again with no new entries changes zero rows
-- (WHERE ... IS DISTINCT FROM ... guards both statements).

-- Accounts WITH entries: balance = credit - debit.
UPDATE account_balances ab
SET balance = agg.computed, updated_at = now()
FROM (
    SELECT account_id,
           COALESCE(SUM(amount) FILTER (WHERE direction = 'credit'), 0) -
           COALESCE(SUM(amount) FILTER (WHERE direction = 'debit'),  0) AS computed
    FROM ledger_entries
    GROUP BY account_id
) agg
WHERE ab.account_id = agg.account_id
  AND ab.balance IS DISTINCT FROM agg.computed;

-- Accounts WITHOUT any entries: balance must be 0.
UPDATE account_balances ab
SET balance = 0, updated_at = now()
WHERE ab.balance != 0
  AND NOT EXISTS (
      SELECT 1 FROM ledger_entries le WHERE le.account_id = ab.account_id
  );
