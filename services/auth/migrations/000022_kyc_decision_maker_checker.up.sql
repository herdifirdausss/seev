-- A11 Round 3: every KYC terminal decision has a durable authorization
-- record. Human decisions require two distinct identities. Automated
-- provider/sanctions decisions use separate machine requester/checker
-- identities so they remain attributable without pretending to be manual.
CREATE TABLE kyc_decision_requests (
    id                   UUID        PRIMARY KEY,
    submission_id        UUID        NOT NULL REFERENCES kyc_submissions(id) ON DELETE CASCADE,
    requested_decision   TEXT        NOT NULL CHECK (requested_decision IN ('approve', 'reject')),
    request_reason       TEXT        NOT NULL CHECK (length(btrim(request_reason)) > 0),
    requested_by         TEXT        NOT NULL CHECK (length(btrim(requested_by)) > 0),
    status               TEXT        NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'authorized', 'rejected')),
    decided_by           TEXT        NULL,
    review_reason        TEXT        NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at           TIMESTAMPTZ NULL,
    applied_at           TIMESTAMPTZ NULL,

    CONSTRAINT chk_kyc_decision_distinct_actors
        CHECK (decided_by IS NULL OR decided_by <> requested_by),
    CONSTRAINT chk_kyc_decision_state
        CHECK (
            (status = 'pending'
                AND decided_by IS NULL
                AND review_reason IS NULL
                AND decided_at IS NULL
                AND applied_at IS NULL)
            OR
            (status = 'authorized'
                AND decided_by IS NOT NULL
                AND decided_at IS NOT NULL
                AND review_reason IS NULL)
            OR
            (status = 'rejected'
                AND decided_by IS NOT NULL
                AND decided_at IS NOT NULL
                AND review_reason IS NOT NULL
                AND length(btrim(review_reason)) > 0
                AND applied_at IS NULL)
        )
);

CREATE UNIQUE INDEX uq_kyc_decision_requests_one_active
    ON kyc_decision_requests (submission_id)
    WHERE status = 'pending' OR (status = 'authorized' AND applied_at IS NULL);
CREATE INDEX idx_kyc_decision_requests_queue
    ON kyc_decision_requests (status, created_at, id);
CREATE INDEX idx_kyc_decision_requests_submission
    ON kyc_decision_requests (submission_id, created_at DESC, id DESC);

ALTER TABLE kyc_submissions
    ADD COLUMN decision_request_id UUID NULL REFERENCES kyc_decision_requests(id) ON DELETE SET NULL;
ALTER TABLE kyc_apply_retries
    ADD COLUMN decision_request_id UUID NULL REFERENCES kyc_decision_requests(id) ON DELETE SET NULL;

-- Preserve any upgrade retry that was already pending when this migration
-- lands. It receives an explicitly identified legacy machine authorization;
-- new code always creates the request before queueing a retry.
INSERT INTO kyc_decision_requests
    (id, submission_id, requested_decision, request_reason, requested_by,
     status, decided_by, created_at, decided_at)
SELECT DISTINCT ON (r.submission_id)
       gen_random_uuid(), r.submission_id, 'approve',
       'legacy KYC apply retry authorized before maker-checker migration',
       'legacy-kyc-apply', 'authorized', 'migration-000022',
       r.created_at, now()
FROM kyc_apply_retries r
JOIN kyc_submissions s ON s.id = r.submission_id AND s.status = 'pending'
WHERE r.submission_id IS NOT NULL
  AND r.direction = 'upgrade'
  AND r.status = 'pending'
  AND NOT EXISTS (
      SELECT 1 FROM kyc_decision_requests d
      WHERE d.submission_id = r.submission_id
  )
ORDER BY r.submission_id, r.created_at ASC, r.id ASC;

UPDATE kyc_apply_retries r
SET decision_request_id = d.id
FROM kyc_decision_requests d
WHERE r.submission_id = d.submission_id
  AND r.direction = 'upgrade'
  AND r.status = 'pending'
  AND r.decision_request_id IS NULL;

-- Close the read-then-insert race in RequestKYCDecision. The row lock makes
-- request creation/authorization serialize with a concurrent terminal update
-- of the submission, while a rejected proposal still permits a later request.
CREATE FUNCTION fn_enforce_kyc_decision_request_state() RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_submission_status TEXT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND NOT (OLD.status = 'pending' AND NEW.status = 'authorized') THEN
		RETURN NEW;
    END IF;

	SELECT status INTO v_submission_status
	FROM public.kyc_submissions
	WHERE id = NEW.submission_id
	FOR KEY SHARE;

	IF NOT FOUND OR v_submission_status <> 'pending' THEN
		RAISE EXCEPTION 'KYC decision request requires a pending submission'
			USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_kyc_decision_request_state
    BEFORE INSERT OR UPDATE OF status ON kyc_decision_requests
    FOR EACH ROW EXECUTE FUNCTION fn_enforce_kyc_decision_request_state();

-- app_service may only move a KYC submission to a terminal state through an
-- authorized request for the same submission and requested outcome. Schema
-- owners remain able to repair fixtures/data during controlled operations;
-- ordinary runtime credentials cannot bypass the two-actor boundary with raw
-- SQL or a future alternate repository path.
CREATE FUNCTION fn_enforce_kyc_decision_authorization() RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_requested_decision TEXT;
    v_expected_decision  TEXT;
    v_status             TEXT;
    v_decided_by         TEXT;
BEGIN
    IF NOT pg_has_role(current_user, 'app_service', 'member') THEN
        RETURN NEW;
    END IF;

	IF TG_OP = 'INSERT' THEN
	    IF NEW.status NOT IN ('approved', 'rejected') THEN
	        RETURN NEW;
	    END IF;
	ELSIF OLD.status <> 'pending'
	      OR NEW.status NOT IN ('approved', 'rejected')
	      OR NEW.status IS NOT DISTINCT FROM OLD.status THEN
	    RETURN NEW;
	END IF;

        IF NEW.decision_request_id IS NULL THEN
            RAISE EXCEPTION 'KYC decision authorization is required'
                USING ERRCODE = '23514';
        END IF;

        v_expected_decision := CASE
            WHEN NEW.status = 'approved' THEN 'approve'
            ELSE 'reject'
        END;

        SELECT requested_decision, status, decided_by
        INTO v_requested_decision, v_status, v_decided_by
        FROM public.kyc_decision_requests
        WHERE id = NEW.decision_request_id
          AND submission_id = NEW.id;

        IF NOT FOUND
           OR v_status <> 'authorized'
           OR v_requested_decision <> v_expected_decision
           OR NEW.decided_by IS DISTINCT FROM v_decided_by THEN
            RAISE EXCEPTION 'KYC decision authorization does not match submission outcome'
                USING ERRCODE = '23514';
        END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_kyc_decision_authorization
	BEFORE INSERT OR UPDATE OF status ON kyc_submissions
    FOR EACH ROW EXECUTE FUNCTION fn_enforce_kyc_decision_authorization();

GRANT SELECT, INSERT, UPDATE ON kyc_decision_requests TO app_service;
GRANT SELECT ON kyc_decision_requests TO app_readonly;

ALTER TABLE kyc_decision_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE kyc_decision_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON kyc_decision_requests
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON kyc_decision_requests
    FOR SELECT TO app_readonly USING (true);
