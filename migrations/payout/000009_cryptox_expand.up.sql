-- docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4 (K2/K3 expand phase): nullable
-- ciphertext/key-version columns for payout_requests.destination.
-- Plaintext `destination` stays in place and required — application code
-- dual-writes both during this phase (K3 step 2); the contract phase is
-- T2.5/T2.6.
ALTER TABLE payout_requests
    ADD COLUMN destination_ciphertext BYTEA,
    ADD COLUMN destination_key_version INT;

-- No grant changes: app_service already has table-level UPDATE/INSERT
-- (migration 000001) — newly added columns are already covered.
