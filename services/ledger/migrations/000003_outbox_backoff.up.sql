-- Outbox exponential backoff (docs/roadmap/archive/12 Task T2).
-- next_attempt_at gates ClaimFailedForRetry: NULL means "eligible
-- immediately" (a failed event that has never had a backoff computed for
-- it yet, or has been reaped and is ready to retry right away).
ALTER TABLE outbox_events
    ADD COLUMN next_attempt_at TIMESTAMPTZ NULL;

-- Replaces the old idx_outbox_retry (last_attempted_at-ordered) — retry
-- eligibility is now driven by next_attempt_at, not last_attempted_at.
DROP INDEX IF EXISTS idx_outbox_retry;

CREATE INDEX idx_outbox_retry ON outbox_events(next_attempt_at ASC NULLS FIRST)
    WHERE status = 'failed';
