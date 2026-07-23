-- docs/roadmap/archive/16 Task T2 (K5): external reconciliation table and suspense accounts.

-- 'suspense' is added as a new system-account type — reconciliation differences
-- are parked here until resolved through maker-checker (T1), NOT in the global
-- 'adjustment' account, which already has a different meaning.
ALTER TABLE accounts DROP CONSTRAINT accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment','suspense'));

-- One suspense account per gateway (the 000002 seed pattern) — allow_negative
-- is required because a difference can mean that the ledger has more or less
-- funds than the gateway settlement report.
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by) VALUES
('00000000-0000-0000-0000-000000000010','system','suspense','IDR','suspense:bca',      'migration'),
('00000000-0000-0000-0000-000000000011','system','suspense','IDR','suspense:gopay',    'migration'),
('00000000-0000-0000-0000-000000000012','system','suspense','IDR','suspense:platform', 'migration');

INSERT INTO account_balances (account_id, allow_negative)
SELECT id, true FROM accounts WHERE type = 'suspense';

-- One batch = one gateway settlement CSV file for one report_date.
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

-- One row = one external_ref from the gateway report OR one internal
-- transaction with an external_ref missing from the report
-- (match_status='missing_external', inserted by the matcher, not from CSV —
-- see step 4 in 16-T2).
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
