-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T5b (K10, K11): screening_events only
-- grants app_service SELECT+INSERT (000001) — deliberately no UPDATE,
-- since this table is meant to stay append-only historical audit. Rather
-- than widen that grant for one narrow use case, this is a SECURITY
-- DEFINER function performing ONLY the exact repoint the closure saga
-- needs (subject -> surrogate), mirroring this codebase's own retention
-- purge/redact functions' "keep app_service's raw privileges minimal,
-- channel privileged mutations through a narrow function" convention.
CREATE OR REPLACE FUNCTION fn_privacy_closure_repoint_screening_events(
    p_subject UUID, p_surrogate UUID
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
BEGIN
    UPDATE screening_events SET user_id = p_surrogate WHERE user_id = p_subject;
    GET DIAGNOSTICS v_affected = ROW_COUNT;
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_privacy_closure_repoint_screening_events(UUID, UUID) TO app_service;
