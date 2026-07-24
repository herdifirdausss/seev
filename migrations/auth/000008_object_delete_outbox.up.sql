-- docs/roadmap/active/51-a8-data-lifecycle-privacy.md T1.6 (K6): a generic
-- object-delete outbox, ahead of any real KYC/export object cleanup policy
-- (T2/T4 own that). K6: "Object deletion uses an outbox: first persist a
-- delete intent, then delete the encrypted object idempotently, then mark
-- metadata redacted/deleted. A storage outage never causes metadata to
-- claim that an object was removed."
--
-- No DELETE grant is needed anywhere here — pkg/objectoutbox.Worker only
-- ever INSERTs an intent and UPDATEs status/metadata, matching K4's
-- append-only-audit philosophy (the outbox row itself is never removed,
-- even once 'done').
CREATE TABLE auth_object_delete_outbox (
    id          UUID PRIMARY KEY,
    ref_table   TEXT NOT NULL,
    ref_id      UUID NOT NULL,
    object_key  TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'done')),
    attempts    INT NOT NULL DEFAULT 0,
    last_error  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (ref_table, ref_id)
);
CREATE INDEX idx_auth_object_delete_outbox_pending ON auth_object_delete_outbox (created_at) WHERE status = 'pending';

-- The metadata-side "redacted/deleted" marker K6 requires. Never set except
-- by pkg/objectoutbox.Worker, and only after the object store itself
-- confirms the object is gone.
ALTER TABLE kyc_documents ADD COLUMN deleted_at TIMESTAMPTZ;

GRANT SELECT, INSERT, UPDATE ON auth_object_delete_outbox TO app_service;
GRANT SELECT ON auth_object_delete_outbox TO app_readonly;

ALTER TABLE auth_object_delete_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_object_delete_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON auth_object_delete_outbox
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON auth_object_delete_outbox
    FOR SELECT TO app_readonly USING (true);
