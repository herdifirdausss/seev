-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.4 (K2/K3 expand phase): nullable
-- ciphertext/key-version columns for sessions.email. Plaintext `email`
-- stays in place and required — application code dual-writes both during
-- this phase (K3 step 2); the contract phase is T2.5/T2.6.
--
-- audit_log.email deliberately gets NO new column here: K2's own wording
-- for that field is "masked ... audit identity", not ciphertext — a
-- one-way transform (internal/platform/security/crypto.MaskEmail) applied at write time going
-- forward (services/adminbff/internal/admin/audit.go), never decrypted back. There is
-- nothing to expand/backfill/contract for a value that was never meant to
-- be recoverable.
ALTER TABLE sessions
    ADD COLUMN email_ciphertext  BYTEA,
    ADD COLUMN email_key_version INT;

-- No grant changes: app_service already has table-level UPDATE/INSERT on
-- sessions (migration 000001) — newly added columns are already covered.
