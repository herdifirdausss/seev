-- ============================================================================
-- SEEV LEDGER — skema kanonik v1
-- Bentuk mengikuti kode Go (internal/ledger). Lihat docs/plan/04.
-- ============================================================================

-- ── ACCOUNTS ────────────────────────────────────────────────────────────────
-- owner_id tanpa FK: user dikelola modul auth (belum ada); integritas di app.
-- system_qualifier: shard key akun sistem (settlement per gateway, fee per
-- gateway, escrow per currency, chargeback per card network). NULL utk akun user.
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

    -- akun sistem tidak punya owner; akun non-sistem tidak punya qualifier
    CONSTRAINT chk_system_shape CHECK (
        (owner_type = 'system' AND owner_id IS NULL)
        OR (owner_type <> 'system' AND system_qualifier IS NULL)
    )
);

-- satu akun per (owner, type, currency, pocket)
CREATE UNIQUE INDEX uq_accounts_owner_pocket
    ON accounts(owner_type, owner_id, type, currency, pocket_code)
    WHERE pocket_code IS NOT NULL;
CREATE UNIQUE INDEX uq_accounts_owner
    ON accounts(owner_type, owner_id, type, currency)
    WHERE pocket_code IS NULL AND owner_id IS NOT NULL;
-- satu akun sistem per (type, currency, qualifier)
CREATE UNIQUE INDEX uq_accounts_system
    ON accounts(type, currency, COALESCE(system_qualifier, ''))
    WHERE owner_type = 'system';

CREATE INDEX idx_accounts_owner  ON accounts(owner_id) WHERE owner_id IS NOT NULL;
CREATE INDEX idx_accounts_status ON accounts(status)   WHERE status <> 'active';

-- ── ACCOUNT BALANCES (projection; kebenaran = ledger_entries) ───────────────
-- allow_negative: true HANYA untuk akun sistem yang secara desain "berhutang"
-- ke dunia luar (settlement, adjustment, chargeback) — lihat 000002 seed data.
CREATE TABLE account_balances (
    account_id     UUID        PRIMARY KEY REFERENCES accounts(id),
    balance        BIGINT      NOT NULL DEFAULT 0,
    allow_negative BOOLEAN     NOT NULL DEFAULT false,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_balance_floor CHECK (allow_negative OR balance >= 0)
);

-- ── LEDGER TRANSACTIONS (header) ─────────────────────────────────────────────
-- source_account_id/destination_account_id adalah kolom INFORMATIF, bukan
-- sumber kebenaran (itu tetap ledger_entries + balance_after). Sejak
-- docs/plan/14 Task T1 (2026-07-11) diisi eksplisit dari
-- TxProcessor.ResolveAccounts's ResolvedAccounts.Source/Destination — NULL
-- untuk processor yang gerakan dananya bukan satu pasang source->destination
-- (mis. Reversal multi-akun). Baris yang ditulis SEBELUM cutoff ini (oleh
-- kode lama yang mengisi dari AccountIDs[0..1] posisional) tidak di-backfill
-- — nilainya tetap seperti apa adanya, hanya baris baru yang semantik.
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

-- [D4] NULL scope harus tetap unik → COALESCE, bukan UNIQUE constraint biasa
CREATE UNIQUE INDEX uq_ltx_idempotency
    ON ledger_transactions(idempotency_key, COALESCE(idempotency_scope, ''));

CREATE INDEX idx_ltx_src  ON ledger_transactions(source_account_id, created_at DESC)
    WHERE source_account_id IS NOT NULL;
CREATE INDEX idx_ltx_dest ON ledger_transactions(destination_account_id, created_at DESC)
    WHERE destination_account_id IS NOT NULL;
CREATE INDEX idx_ltx_status_pending ON ledger_transactions(created_at)
    WHERE status = 'pending';

-- ── LEDGER ENTRIES (append-only, sumber kebenaran) ───────────────────────────
-- [D6] balance_after = saldo FINAL akun setelah seluruh transaksi (semua entry
-- akun yang sama dalam satu tx menulis nilai final yang sama). Jangan tambah
-- constraint per-entry balance math.
-- Catatan: TIDAK ada CHECK (balance_after >= 0) di sini — akun sistem dengan
-- allow_negative=true (settlement, adjustment, chargeback) boleh punya
-- balance_after negatif. Guard overdraft untuk akun biasa ada di
-- account_balances.chk_balance_floor + validasi InsufficientBalance di service.
CREATE TABLE ledger_entries (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID        NOT NULL REFERENCES ledger_transactions(id),
    account_id     UUID        NOT NULL REFERENCES accounts(id),
    direction      TEXT        NOT NULL CHECK (direction IN ('debit','credit')),
    amount         BIGINT      NOT NULL CHECK (amount > 0),
    balance_after  BIGINT      NOT NULL,
    note           TEXT        NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (transaction_id, account_id, direction)
);

CREATE INDEX idx_entries_account ON ledger_entries(account_id, created_at DESC);
CREATE INDEX idx_entries_tx      ON ledger_entries(transaction_id);
-- covering index untuk fn_verify_ledger_balance
CREATE INDEX idx_entries_verify  ON ledger_entries(created_at, transaction_id)
    INCLUDE (direction, amount);

-- immutability: UPDATE/DELETE dilarang di level DB
CREATE OR REPLACE FUNCTION fn_prevent_entry_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is immutable — use a correcting transaction';
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_entries_immutable
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION fn_prevent_entry_mutation();

-- ── OUTBOX EVENTS ────────────────────────────────────────────────────────────
-- Kode hanya meng-insert (id, aggregate_type, aggregate_id, event_type,
-- payload, created_at) — kolom lain WAJIB punya DEFAULT.
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

-- auto dead-letter: failed + retry habis → dead
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

-- ── FUNGSI VERIFIKASI (dipakai job harian, 06-phase-1-workers) ───────────────
-- Transaksi posted yang debit ≠ credit dalam window waktu. Output HARUS kosong.
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

-- Saldo tersimpan vs saldo hasil hitung ulang dari entries (per akun).
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

-- View audit semua akun yang bergerak 24 jam terakhir
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

-- ── HARDENING MINIMAL (RLS penuh ditunda ke Phase 2, keputusan D11) ─────────
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
