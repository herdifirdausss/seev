-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.4 (K2/K3 expand phase): nullable
-- ciphertext/key-version columns for payin_webhook_events.raw. Plaintext
-- `raw` stays in place and required — application code dual-writes both
-- during this phase (K3 step 2); the contract phase (making ciphertext
-- required, redacting plaintext) is T2.5/T2.6.
ALTER TABLE payin_webhook_events
    ADD COLUMN raw_ciphertext  BYTEA,
    ADD COLUMN raw_key_version INT;

-- No grant changes: app_service already has table-level UPDATE/INSERT
-- (migration 000001) — newly added columns are already covered.
