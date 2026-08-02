-- idx_sanctions_entries_name (normalized_name) is a strict prefix of
-- idx_sanctions_entries_name_birth (normalized_name, birth_date) —
-- Postgres can satisfy any WHERE normalized_name = $1 query from the wider
-- two-column index, so the single-column one only adds write overhead. This
-- table is bulk-loaded/refreshed by the OpenSanctions loader per its own
-- comment, so every load paid for maintaining a fully redundant index with
-- zero read benefit (schema audit finding, duplication pass).
DROP INDEX idx_sanctions_entries_name;
