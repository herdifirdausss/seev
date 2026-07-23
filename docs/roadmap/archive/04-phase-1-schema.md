# 04 — Phase 1a: Canonical Database Schema

Goal: create one set of golang-migrate files that **exactly** matches the SQL
written by the Go code (see 00-current-state.md M1), together with the integrity
guards ported from `docs/design/legacy-schemas/ledgernew.sql`. See
01-target-architecture.md D1–D7 for the design decisions.

Prerequisite: Phase 0 is complete (`migrations/` is empty and the old schemas
are archived).

## Task 1a.1 — Migration `migrations/000001_ledger_core.up.sql`

Write the following file as shown. Comments may be cleaned up, but table names,
column names, and types must not change:

```sql
-- ============================================================================
-- SEEV LEDGER — canonical schema v1
-- Follows the Go code in internal/ledger. See docs/roadmap/archive/04.
-- ============================================================================

-- ── ACCOUNTS ────────────────────────────────────────────────────────────────
-- owner_id has no FK: users are managed by the auth module (not yet present);
-- integrity is enforced by the application.
-- system_qualifier: shard key for system accounts (settlement per gateway,
-- fee per gateway, escrow per currency, chargeback per card network).
-- NULL for user accounts.
CREATE TABLE accounts (
    id               UUID        PRIMARY KEY,
    owner_id         UUID        NULL,
    owner_type       TEXT        NOT NULL CHECK (owner_type IN
                       ('user','system','merchant','partner','escrow')),
    type             TEXT        NOT NULL CHECK (type IN
                       ('cash','hold','pending','frozen','pocket','fee',
                        'settlement','escrow','chargeback','confiscated','adjustment')),
    currency         CHAR(3)     NOT NULL,
    pocket_code      VARCHAR(32) NULL,
    system_qualifier TEXT        NULL,
    status           TEXT        NOT NULL DEFAULT 'active' CHECK (status IN
                       ('active','inactive','closed','suspended')),
    created_by       TEXT        NOT NULL DEFAULT 'system',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- System accounts have no owner; non-system accounts have no qualifier.
    CONSTRAINT chk_system_shape CHECK (
        (owner_type = 'system' AND owner_id IS NULL)
        OR (owner_type <> 'system' AND system_qualifier IS NULL)
    )
);

-- One account per (owner, type, currency, pocket).
CREATE UNIQUE INDEX uq_accounts_owner_pocket
    ON accounts(owner_type, owner_id, type, currency, pocket_code)
    WHERE pocket_code IS NOT NULL;
CREATE UNIQUE INDEX uq_accounts_owner
    ON accounts(owner_type, owner_id, type, currency)
    WHERE pocket_code IS NULL AND owner_id IS NOT NULL;
-- One system account per (type, currency, qualifier).
CREATE UNIQUE INDEX uq_accounts_system
    ON accounts(type, currency, COALESCE(system_qualifier, ''))
    WHERE owner_type = 'system';

CREATE INDEX idx_accounts_owner  ON accounts(owner_id) WHERE owner_id IS NOT NULL;
CREATE INDEX idx_accounts_status ON accounts(status)   WHERE status <> 'active';

-- ── ACCOUNT BALANCES (projection; source of truth = ledger_entries) ─────────
CREATE TABLE account_balances (
    account_id UUID        PRIMARY KEY REFERENCES accounts(id),
    balance    BIGINT      NOT NULL DEFAULT 0 CHECK (balance >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── LEDGER TRANSACTIONS (header) ─────────────────────────────────────────────
-- Note: source/destination are filled from AccountIDs[0..1] AFTER sorting;
-- they have no semantic meaning for multi-account transactions. The semantics
-- live in ledger_entries. Semantic correction is planned for Phase 2
-- (07-phase-2 Task H6).
CREATE TABLE ledger_transactions (
    id                     UUID        PRIMARY KEY,
    idempotency_key        TEXT        NOT NULL,
    idempotency_scope      TEXT        NULL,
    type                   TEXT        NOT NULL,
    status                 TEXT        NOT NULL CHECK (status IN ('pending','posted','failed','reversed')),
    amount                 BIGINT      NOT NULL CHECK (amount > 0),
    currency               CHAR(3)     NOT NULL,
    source_account_id      UUID        NULL REFERENCES accounts(id),
    destination_account_id UUID        NULL REFERENCES accounts(id),
    error_message          TEXT        NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- [D4] A NULL scope must remain unique: use COALESCE rather than a normal
-- UNIQUE constraint.
CREATE UNIQUE INDEX uq_ltx_idempotency
    ON ledger_transactions(idempotency_key, COALESCE(idempotency_scope, ''));

CREATE INDEX idx_ltx_src  ON ledger_transactions(source_account_id, created_at DESC)
    WHERE source_account_id IS NOT NULL;
CREATE INDEX idx_ltx_dest ON ledger_transactions(destination_account_id, created_at DESC)
    WHERE destination_account_id IS NOT NULL;
CREATE INDEX idx_ltx_status_pending ON ledger_transactions(created_at)
    WHERE status = 'pending';

-- ── LEDGER ENTRIES (append-only, source of truth) ────────────────────────────
-- [D6] balance_after is the account's FINAL balance after the entire
-- transaction. Every entry for the same account in one transaction writes the
-- same final value. Do not add a per-entry balance-math constraint.
CREATE TABLE ledger_entries (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID        NOT NULL REFERENCES ledger_transactions(id),
    account_id     UUID        NOT NULL REFERENCES accounts(id),
    direction      TEXT        NOT NULL CHECK (direction IN ('debit','credit')),
    amount         BIGINT      NOT NULL CHECK (amount > 0),
    balance_after  BIGINT      NOT NULL CHECK (balance_after >= 0),
    note           TEXT        NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (transaction_id, account_id, direction)
);

CREATE INDEX idx_entries_account ON ledger_entries(account_id, created_at DESC);
CREATE INDEX idx_entries_tx      ON ledger_entries(transaction_id);
-- Covering index for fn_verify_ledger_balance.
CREATE INDEX idx_entries_verify  ON ledger_entries(created_at, transaction_id)
    INCLUDE (direction, amount);

-- Immutability: UPDATE and DELETE are forbidden at the database level.
CREATE OR REPLACE FUNCTION fn_prevent_entry_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is immutable — use a correcting transaction';
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_entries_immutable
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION fn_prevent_entry_mutation();

-- ── OUTBOX EVENTS ────────────────────────────────────────────────────────────
-- The code inserts only (id, aggregate_type, aggregate_id, event_type,
-- payload, created_at); every other column MUST have a DEFAULT.
CREATE TABLE outbox_events (
    id                UUID        PRIMARY KEY,
    aggregate_type    TEXT        NOT NULL,
    aggregate_id      UUID        NOT NULL,
    event_type        TEXT        NOT NULL,
    payload           JSONB       NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN
                        ('pending','processing','published','failed','dead')),
    retry_count       INT         NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    max_retries       INT         NOT NULL DEFAULT 5 CHECK (max_retries > 0),
    last_error        TEXT        NULL,
    last_attempted_at TIMESTAMPTZ NULL,
    published_at      TIMESTAMPTZ NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_published_at   CHECK (published_at IS NULL OR status = 'published'),
    CONSTRAINT chk_last_attempted CHECK (retry_count = 0 OR last_attempted_at IS NOT NULL)
);

CREATE INDEX idx_outbox_pending    ON outbox_events(created_at ASC)
    WHERE status = 'pending';
CREATE INDEX idx_outbox_retry      ON outbox_events(last_attempted_at ASC NULLS FIRST)
    WHERE status = 'failed';
CREATE INDEX idx_outbox_processing ON outbox_events(last_attempted_at ASC)
    WHERE status = 'processing';
CREATE INDEX idx_outbox_dead       ON outbox_events(created_at DESC)
    WHERE status = 'dead';
CREATE INDEX idx_outbox_aggregate  ON outbox_events(aggregate_id, aggregate_type);

-- Automatic dead-lettering: failed + retries exhausted → dead.
CREATE OR REPLACE FUNCTION fn_outbox_check_dead_letter() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = 'failed' AND NEW.retry_count >= NEW.max_retries THEN
        NEW.status = 'dead';
    END IF;
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_outbox_dead_letter
    BEFORE UPDATE ON outbox_events
    FOR EACH ROW WHEN (NEW.status = 'failed')
    EXECUTE FUNCTION fn_outbox_check_dead_letter();

-- ── updated_at triggers ──────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION fn_set_updated_at() RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_accounts_ua BEFORE UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_ltx_ua BEFORE UPDATE ON ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
CREATE TRIGGER trg_balances_ua BEFORE UPDATE ON account_balances
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ── VERIFICATION FUNCTIONS (used by the daily job, 06-phase-1-workers) ──────
-- Posted transactions whose debit and credit totals differ in the time window.
-- The output MUST be empty.
CREATE OR REPLACE FUNCTION fn_verify_ledger_balance(
    p_from TIMESTAMPTZ DEFAULT now() - INTERVAL '1 day',
    p_to   TIMESTAMPTZ DEFAULT now()
)
RETURNS TABLE (transaction_id UUID, sum_debit BIGINT, sum_credit BIGINT, diff BIGINT)
LANGUAGE sql STABLE AS $$
    SELECT
        transaction_id,
        COALESCE(SUM(amount) FILTER (WHERE direction = 'debit'),  0) AS sum_debit,
        COALESCE(SUM(amount) FILTER (WHERE direction = 'credit'), 0) AS sum_credit,
        COALESCE(SUM(amount) FILTER (WHERE direction = 'debit'),  0) -
        COALESCE(SUM(amount) FILTER (WHERE direction = 'credit'), 0) AS diff
    FROM ledger_entries
    WHERE created_at BETWEEN p_from AND p_to
    GROUP BY transaction_id
    HAVING COALESCE(SUM(amount) FILTER (WHERE direction = 'debit'),  0)
        IS DISTINCT FROM
           COALESCE(SUM(amount) FILTER (WHERE direction = 'credit'), 0);
$$;

-- Stored balance versus the balance recomputed from entries (per account).
CREATE OR REPLACE FUNCTION fn_verify_account_balance(p_account_id UUID)
RETURNS TABLE (account_id UUID, stored_balance BIGINT, computed_balance BIGINT,
               diff BIGINT, is_consistent BOOLEAN)
LANGUAGE sql STABLE AS $$
    SELECT
        ab.account_id,
        ab.balance,
        COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'credit'), 0) -
        COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'debit'),  0) AS computed,
        ab.balance - (
          COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'credit'), 0) -
          COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'debit'),  0)) AS diff,
        ab.balance = (
          COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'credit'), 0) -
          COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'debit'),  0)) AS is_consistent
    FROM account_balances ab
    LEFT JOIN ledger_entries le ON le.account_id = ab.account_id
    WHERE ab.account_id = p_account_id
    GROUP BY ab.account_id, ab.balance;
$$;

-- Audit view for all accounts active in the last 24 hours.
CREATE VIEW v_account_balance_audit AS
SELECT a.id AS account_id, a.owner_type, a.type, a.currency,
       ab.balance AS stored_balance,
       COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'credit'), 0) -
       COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'debit'),  0) AS computed_balance,
       ab.balance = COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'credit'), 0) -
                    COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'debit'),  0) AS is_consistent,
       ab.updated_at AS last_balance_update
FROM accounts a
JOIN account_balances ab ON ab.account_id = a.id
LEFT JOIN ledger_entries le ON le.account_id = a.id
WHERE ab.updated_at > now() - INTERVAL '1 day'
GROUP BY a.id, a.owner_type, a.type, a.currency, ab.balance, ab.updated_at;

-- ── MINIMAL HARDENING (full RLS deferred to Phase 2, decision D11) ──────────
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
```

## Task 1a.2 — Migration `migrations/000002_seed_system_accounts.up.sql`

```sql
-- Initial system accounts. Add a new gateway or currency with a new migration.
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by) VALUES
('00000000-0000-0000-0000-000000000001','system','settlement','IDR','bca',      'migration'),
('00000000-0000-0000-0000-000000000002','system','settlement','IDR','gopay',    'migration'),
('00000000-0000-0000-0000-000000000003','system','fee',       'IDR','platform', 'migration'),
('00000000-0000-0000-0000-000000000004','system','fee',       'IDR','bca',      'migration'),
('00000000-0000-0000-0000-000000000005','system','fee',       'IDR','gopay',    'migration'),
('00000000-0000-0000-0000-000000000006','system','escrow',    'IDR','IDR',      'migration'),
('00000000-0000-0000-0000-000000000007','system','chargeback','IDR','visa',     'migration'),
('00000000-0000-0000-0000-000000000008','system','adjustment','IDR',NULL,       'migration'),
('00000000-0000-0000-0000-000000000009','system','confiscated','IDR',NULL,      'migration');

INSERT INTO account_balances (account_id)
SELECT id FROM accounts WHERE owner_type = 'system';
```

Note: `settlement` and `adjustment` accounts can be negative by design because
they represent money entering from outside the system, but the
`CHECK (balance >= 0)` currently applies globally. **For MVP**, either seed
settlement and adjustment balances through a technical top-up, OR (the preferred
option) exclude system accounts from the check in `000001` by adding an
`allow_negative BOOLEAN NOT NULL DEFAULT false` column and a validation check:

```sql
-- Replacement for CHECK (balance >= 0) on account_balances:
ALTER TABLE account_balances ADD COLUMN allow_negative BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE account_balances DROP CONSTRAINT account_balances_balance_check;
ALTER TABLE account_balances ADD CONSTRAINT chk_balance_floor
    CHECK (allow_negative OR balance >= 0);
```
Set `allow_negative = true` for `settlement`, `adjustment`, and `chargeback`
accounts in the seed. Merge this directly into `000001` rather than creating a
separate `ALTER`; it is shown separately here only to explain the rationale.
The Go code does not read `allow_negative` (it is a database-only guard). Also
remove the `ledger_entries.balance_after >= 0` check: the overdraft guard
remains `chk_balance_floor` on the projection plus processor-level
`InsufficientBalance` validation.

## Task 1a.3 — `.down.sql` files

`000001_ledger_core.down.sql`: drop the view, functions, triggers, and tables in
reverse dependency order (outbox → entries → transactions → balances →
accounts → functions). `000002_seed_system_accounts.down.sql` must delete the
seeded system balances and accounts.

## Task 1a.4 — Code changes required by the schema

1. **`internal/ledger/repository/account_balance_repository.go`** — in the
   `LockBalances` query, change `ab.currency` to `a.currency` (decision D3).
   The scan columns do not change.
2. **`internal/ledger/processors/validators.go`** — ensure integral amounts are
   validated with `if !cmd.Amount.IsInteger() { return ErrValidation }` (amounts
   are minor units, D2). Add the validation and a test if missing.
3. **Makefile** — ensure `migrate-up` and `migrate-down` use the
   golang-migrate CLI against `migrations/` and the `POSTGRES_*` environment.

## Task 1a.5 — Schema-to-code contract test

Create `internal/ledger/schema_contract_test.go` with the `integration` build
tag and testcontainers PostgreSQL:

1. Start a PostgreSQL container and run every `migrations/*.up.sql` file in
   order.
2. Insert a user account and balance directly with SQL.
3. Run the real `Service.Handle` with real repositories, not mocks, for
   `money_in` → `transfer_p2p` (create two users) → `money_out`.
4. Assert that the final balances are correct,
   `SELECT * FROM fn_verify_ledger_balance('-infinity','infinity')` is empty,
   and `fn_verify_account_balance` is consistent for every involved account.
5. Send the same command twice and confirm that no second transaction row is
   created.
6. Try `UPDATE ledger_entries SET amount = 1` and confirm that the trigger
   rejects it.

## Definition of done for 04

- [ ] `make migrate-up` succeeds on an empty database, and `make migrate-down`
      returns it to empty.
- [ ] Contract test (1a.5) passes:
      `go test -tags integration ./internal/ledger/ -run TestSchemaContract -race`.
- [ ] The M1 table/column audit in 00-current-state.md confirms that every
      column referenced by the code exists in the migrations.
