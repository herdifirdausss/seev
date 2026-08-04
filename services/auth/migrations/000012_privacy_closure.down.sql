ALTER TABLE auth_users DROP CONSTRAINT auth_users_status_check;
ALTER TABLE auth_users ADD CONSTRAINT auth_users_status_check CHECK (status IN ('active', 'disabled'));

DROP INDEX idx_privacy_requests_closure_pending;

DROP INDEX uq_privacy_requests_active_per_user;
CREATE UNIQUE INDEX uq_privacy_requests_active_per_user
    ON privacy_requests (user_id)
    WHERE status IN ('pending', 'collecting');

ALTER TABLE privacy_requests DROP CONSTRAINT privacy_requests_status_check;
ALTER TABLE privacy_requests ADD CONSTRAINT privacy_requests_status_check CHECK (status IN
    ('pending', 'collecting', 'ready', 'failed', 'expired'));

ALTER TABLE privacy_requests
    DROP COLUMN request_type,
    DROP COLUMN surrogate_id,
    DROP COLUMN active_subject_ciphertext,
    DROP COLUMN owner_checkpoints,
    DROP COLUMN retry_count,
    DROP COLUMN next_attempt_at,
    DROP COLUMN last_error;
