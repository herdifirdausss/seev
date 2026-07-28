-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3 (K7): idempotency-key digest
-- tombstone. idempotency_key_digest is a keyed HMAC-SHA256 over a
-- canonical, length-delimited (scope, key) value (pkg/cryptox.DigestRing) —
-- unlike idempotency_key/idempotency_scope themselves (raw, purgeable
-- after 30 days by a later retention class), this digest is PERMANENT: it
-- is what still enforces "the same idempotency key can never post twice"
-- after the raw value is gone. idempotency_key_version records which
-- digest-ring key version produced it, so rotation can tell an
-- already-migrated row from one still needing backfill.
--
-- conflict_fingerprint is a plain SHA-256 (no secret key — it never needs
-- to resist offline guessing, only exact-match comparison against itself)
-- over (type, amount, currency): the first time this codebase actually
-- detects "same idempotency key reused with different business
-- parameters" as a DISTINCT outcome from "same key, same request,
-- legitimate retry" — previously handleDuplicate only ever compared
-- status, never the request itself.
ALTER TABLE ledger_transactions
    ADD COLUMN idempotency_key_digest  BYTEA,
    ADD COLUMN idempotency_key_version INT,
    ADD COLUMN conflict_fingerprint    BYTEA;

-- Partial (WHERE ... IS NOT NULL) so pre-backfill rows with no digest yet
-- never collide with each other on NULL.
CREATE UNIQUE INDEX uq_ltx_idempotency_digest
    ON ledger_transactions(idempotency_key_digest)
    WHERE idempotency_key_digest IS NOT NULL;

-- Ahead of a later retention class nulling idempotency_key/idempotency_scope
-- after 30 days (docs/roadmap/archive/51 T3 item 5) — the existing
-- uq_ltx_idempotency (idempotency_key, COALESCE(idempotency_scope,'')) index
-- is deliberately left in place, not dropped: during the window before
-- rotation backfill has caught every row up to the current digest key
-- version, it is the ONLY thing that still catches a duplicate whose
-- freshly-computed digest doesn't yet match an old row's stale-version
-- digest (see internal/ledger/repository's own FindConflictOrDuplicate
-- comment). It simply stops being load-bearing, harmlessly, once every
-- row has a current-version digest AND the retention job starts nulling
-- idempotency_key.
ALTER TABLE ledger_transactions ALTER COLUMN idempotency_key DROP NOT NULL;

-- No grant changes: app_service already has table-level UPDATE/INSERT on
-- ledger_transactions — newly added columns are already covered.
