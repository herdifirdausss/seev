-- Business-completeness audit finding: the `chargeback` processor
-- (services/ledger/internal/processors/chargeback.go) only ever posted the money
-- movement (user.cash -> chargeback[card_network]) with dispute_ref as a
-- free-text ledger_entries.note — there was no queryable case record at
-- all: no status lifecycle, no evidence-submission deadline, no link table
-- an ops team could use to find "which disputes are still open" or
-- "which dispute did this chargeback transaction settle". This is that
-- case-management data model. It deliberately does NOT touch the
-- chargeback processor itself or add a vendor/webhook integration —
-- opening/resolving a case and posting the chargeback's money movement
-- remain two separate steps an ops workflow coordinates, the same
-- separation recon.go already established between ResolveItem (creates a
-- pending adjustment) and the adjustment actually posting.
CREATE TABLE chargeback_disputes (
    id                UUID        PRIMARY KEY,
    -- The charge being disputed — mirrors ledger_transactions.closed_by_tx_id's
    -- lifecycle-guard shape (services/ledger/migrations/000004_lifecycle_guard.up.sql)
    -- but is intentionally its own FK, not a reuse of closed_by_tx_id: a
    -- disputed charge is NOT "closed" the way a reversed/refunded one is —
    -- it stays posted and disputable more than once over its lifetime (a
    -- lost dispute can be re-opened on appeal), and the original charge's
    -- money never moves until/unless the chargeback processor's own
    -- transaction posts separately.
    original_tx_id    UUID        NOT NULL REFERENCES ledger_transactions(id),
    -- Set once the `chargeback` processor's forced-debit transaction posts
    -- (an ops step outside this table) — NULL while the case is still under
    -- review with no funds pulled yet.
    chargeback_tx_id  UUID        NULL REFERENCES ledger_transactions(id),
    dispute_ref       TEXT        NOT NULL,
    card_network      TEXT        NOT NULL CHECK (card_network IN ('visa','mastercard','jcb','amex')),
    reason_code       TEXT        NULL,
    amount            BIGINT      NOT NULL CHECK (amount > 0),
    currency          CHAR(3)     NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'open'
                      CHECK (status IN ('open','evidence_submitted','won','lost','expired')),
    evidence_due_at   TIMESTAMPTZ NULL,
    evidence_ref      TEXT        NULL,
    resolved_at       TIMESTAMPTZ NULL,
    resolution_reason TEXT        NULL,
    created_by        TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- resolved_at/resolution_reason are set together with a terminal status
    -- by the same UPDATE (ResolveDispute) — never independently.
    CHECK ((status IN ('won','lost','expired')) = (resolved_at IS NOT NULL))
);

-- One case per external dispute — a card network's own webhook/report retry
-- must not create a duplicate case (idempotency, same convention as recon's
-- external_ref uniqueness).
CREATE UNIQUE INDEX uq_chargeback_disputes_dispute_ref ON chargeback_disputes(dispute_ref);

-- Drives "find every case for this charge" (a charge can accumulate more
-- than one dispute_ref over time — re-presentment, then a second dispute).
CREATE INDEX idx_chargeback_disputes_original_tx ON chargeback_disputes(original_tx_id);

-- Drives the ops queue: open cases ordered by how soon evidence is due.
CREATE INDEX idx_chargeback_disputes_open
    ON chargeback_disputes(evidence_due_at)
    WHERE status IN ('open', 'evidence_submitted');

GRANT SELECT, INSERT, UPDATE ON chargeback_disputes TO app_service;
GRANT SELECT ON chargeback_disputes TO app_readonly;

ALTER TABLE chargeback_disputes ENABLE ROW LEVEL SECURITY;
ALTER TABLE chargeback_disputes FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON chargeback_disputes FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON chargeback_disputes FOR SELECT TO app_readonly USING (true);
