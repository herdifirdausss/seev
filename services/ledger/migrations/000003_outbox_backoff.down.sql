DROP INDEX IF EXISTS idx_outbox_retry;

CREATE INDEX idx_outbox_retry ON outbox_events(last_attempted_at ASC NULLS FIRST)
    WHERE status = 'failed';

ALTER TABLE outbox_events
    DROP COLUMN next_attempt_at;
