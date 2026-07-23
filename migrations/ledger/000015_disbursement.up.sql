-- docs/roadmap/archive/19 Task T2 (S3 butir 2): batch disbursement — one CSV manifest
-- posts many `disbursement` transactions with progress + resume, no new
-- worker (docs/roadmap/archive/13 K5 "jangan tambah worker" — 08's own decision).

CREATE TABLE disbursement_batches (
    id              UUID        PRIMARY KEY,
    source_filename TEXT        NOT NULL,
    row_count       INT         NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'processing'
                    CHECK (status IN ('processing','completed','completed_with_errors')),
    created_by      TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE disbursement_items (
    id           UUID    PRIMARY KEY,
    batch_id     UUID    NOT NULL REFERENCES disbursement_batches(id),
    item_no      INT     NOT NULL,
    user_id      UUID    NOT NULL,
    amount       BIGINT  NOT NULL CHECK (amount > 0),
    note         TEXT    NULL,
    status       TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','posted','failed')),
    error        TEXT    NULL,
    posted_tx_id UUID    NULL REFERENCES ledger_transactions(id),

    UNIQUE (batch_id, item_no)
);

-- Drives POST /admin/disbursements/{id}/run's item-selection query
-- (WHERE batch_id = $1 AND status IN ('pending', ...)).
CREATE INDEX idx_disbursement_items_batch_status ON disbursement_items(batch_id, status);

-- Disbursement source account (docs/roadmap/archive/19 Task T2 step 3 decision:
-- default settlement[platform] per currency — platform funds disbursed to
-- users, sharded like every other settlement account. allow_negative=true,
-- same rationale as every settlement account (money owed to/from the
-- outside world before it's reconciled).
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by) VALUES
('00000000-0000-0000-0000-000000000027','system','settlement','IDR','platform','migration'),
('00000000-0000-0000-0000-000000000028','system','settlement','USD','platform','migration');

INSERT INTO account_balances (account_id, allow_negative) VALUES
('00000000-0000-0000-0000-000000000027', true),
('00000000-0000-0000-0000-000000000028', true);

GRANT SELECT, INSERT, UPDATE ON disbursement_batches TO app_service;
GRANT SELECT ON disbursement_batches TO app_readonly;
GRANT SELECT, INSERT, UPDATE ON disbursement_items TO app_service;
GRANT SELECT ON disbursement_items TO app_readonly;

ALTER TABLE disbursement_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE disbursement_batches FORCE ROW LEVEL SECURITY;
ALTER TABLE disbursement_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE disbursement_items FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON disbursement_batches FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON disbursement_batches FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_all_service ON disbursement_items FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON disbursement_items FOR SELECT TO app_readonly USING (true);
