-- docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4 (K2/K3 expand phase): nullable
-- ciphertext/key-version columns for recon_batches.source_filename and
-- recon_items.raw. Plaintext columns stay in place and required/nullable
-- as before — application code dual-writes both during this phase (K3
-- step 2); the contract phase is T2.5/T2.6.
ALTER TABLE recon_batches
    ADD COLUMN source_filename_ciphertext  BYTEA,
    ADD COLUMN source_filename_key_version INT;

ALTER TABLE recon_items
    ADD COLUMN raw_ciphertext  BYTEA,
    ADD COLUMN raw_key_version INT;

-- No grant changes: app_service already has table-level UPDATE/INSERT on
-- both tables (migration 000008) — newly added columns are already
-- covered.
