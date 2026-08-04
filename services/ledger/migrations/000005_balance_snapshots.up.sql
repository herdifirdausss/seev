-- Daily balance snapshots (docs/roadmap/archive/15 Task T1, decision K6).
--
-- Unlike ledger_entries, this table is NOT append-only-with-trigger — the
-- daily job may DELETE+re-INSERT a date's rows to correct a bad snapshot
-- (e.g. after a projection bug is fixed upstream). Only rows for accounts
-- that had activity on that date are written — a passive account's balance
-- is found via GetLatestBefore walking back to its last snapshot, not by
-- writing a redundant unchanged row every day.
CREATE TABLE account_balance_snapshots (
    account_id      UUID        NOT NULL REFERENCES accounts(id),
    as_of_date      DATE        NOT NULL,
    closing_balance BIGINT      NOT NULL,
    entry_count     INT         NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (account_id, as_of_date)
);

-- Supports GetLatestBefore's "most recent snapshot <= date" lookup.
CREATE INDEX idx_snapshots_account_date ON account_balance_snapshots(account_id, as_of_date DESC);
