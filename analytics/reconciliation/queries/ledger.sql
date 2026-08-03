-- Every query is bounded by the runner's read-only session timeout and by a
-- safe cutoff selected before comparison. No query writes source data.
SELECT count(*), coalesce(sum(amount), 0), coalesce(max(created_at), timestamptz 'epoch')
FROM ledger_transactions
WHERE status = 'posted';

SELECT count(*)
FROM (
  SELECT transaction_id
  FROM ledger_entries
  GROUP BY transaction_id
  HAVING coalesce(sum(amount) FILTER (WHERE direction = 'debit'), 0)
      != coalesce(sum(amount) FILTER (WHERE direction = 'credit'), 0)
) mismatches;
