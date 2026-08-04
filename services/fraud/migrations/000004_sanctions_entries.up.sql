-- Plan 46 T5: locally loaded OpenSanctions subset. Only normalized matching
-- fields are retained; the loader owns dataset refresh/replacement.
CREATE TABLE sanctions_entries (
    id              TEXT PRIMARY KEY,
    source          TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    birth_date      TEXT,
    dataset_version TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sanctions_entries_name ON sanctions_entries (normalized_name);
CREATE INDEX idx_sanctions_entries_name_birth ON sanctions_entries (normalized_name, birth_date);

GRANT SELECT, INSERT, UPDATE, DELETE ON sanctions_entries TO app_service;
GRANT SELECT ON sanctions_entries TO app_readonly;
ALTER TABLE sanctions_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE sanctions_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON sanctions_entries
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON sanctions_entries
    FOR SELECT TO app_readonly USING (true);
