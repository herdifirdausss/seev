-- docs/plan/16 Task T2 (K5): tabel rekonsiliasi eksternal + akun suspense.

-- 'suspense' ditambahkan sebagai type akun sistem baru — selisih hasil
-- rekonsiliasi diparkir di sini sampai diresolusi via maker-checker (T1),
-- BUKAN di akun 'adjustment' global yang sudah punya arti lain.
ALTER TABLE accounts DROP CONSTRAINT accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment','suspense'));

-- Satu akun suspense per gateway (pola seed 000002) — allow_negative karena
-- selisih bisa berarti ledger kelebihan atau kekurangan dana dibanding
-- laporan settlement gateway.
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by) VALUES
('00000000-0000-0000-0000-000000000010','system','suspense','IDR','suspense:bca',      'migration'),
('00000000-0000-0000-0000-000000000011','system','suspense','IDR','suspense:gopay',    'migration'),
('00000000-0000-0000-0000-000000000012','system','suspense','IDR','suspense:platform', 'migration');

INSERT INTO account_balances (account_id, allow_negative)
SELECT id, true FROM accounts WHERE type = 'suspense';

-- Satu batch = satu file CSV settlement gateway untuk satu report_date.
CREATE TABLE recon_batches (
    id              UUID        PRIMARY KEY,
    gateway         TEXT        NOT NULL,
    report_date     DATE        NOT NULL,
    source_filename TEXT        NOT NULL,
    row_count       INT         NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'processing'
                    CHECK (status IN ('processing','completed','failed')),
    created_by      TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_recon_batches_gateway_date ON recon_batches (gateway, report_date);

-- Satu baris = satu external_ref dari laporan gateway ATAU satu tx internal
-- ber-external_ref yang tidak ada di laporan (match_status='missing_external',
-- diinsert oleh matcher, bukan dari CSV — lihat langkah 4 di 16-T2).
CREATE TABLE recon_items (
    id                        UUID        PRIMARY KEY,
    batch_id                  UUID        NOT NULL REFERENCES recon_batches(id),
    external_ref              TEXT        NOT NULL,
    amount                    BIGINT      NOT NULL,
    raw                       JSONB       NULL,
    match_status              TEXT        NOT NULL
                              CHECK (match_status IN
                                  ('matched','missing_internal','missing_external','amount_mismatch')),
    matched_tx_id             UUID        NULL REFERENCES ledger_transactions(id),
    resolved_by_adjustment_id UUID        NULL REFERENCES pending_adjustments(id),
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (batch_id, external_ref)
);

CREATE INDEX idx_recon_items_batch_status ON recon_items (batch_id, match_status);
