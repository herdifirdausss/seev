-- app_service (and therefore adminbff_app, which is only ever granted
-- membership in app_service — scripts/lib.sh ensure_app_role) has never had
-- DELETE on sessions: 000001_core.up.sql grants only SELECT, INSERT, UPDATE.
-- Logout issued a direct `DELETE FROM sessions ...`, which fails with
-- "permission denied for table sessions" on every real deployment path and
-- discarded the error, so a session row silently outlived every logout.
--
-- Rather than widen the blanket DELETE grant (docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
-- K4 establishes narrowly scoped SECURITY DEFINER functions as the intended
-- pattern for this repo), add a single-purpose function that deletes exactly
-- one session by id. It does not accept arbitrary SQL or unbounded criteria.
-- The periodic expired-session cleanup path (CleanupSessions) is intentionally
-- left alone here — Track A8's retention-purge functions own that job.
CREATE FUNCTION fn_delete_session(p_session_id TEXT)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_catalog
AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    DELETE FROM sessions WHERE id = p_session_id;
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count > 0;
END;
$$;

REVOKE ALL ON FUNCTION fn_delete_session(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fn_delete_session(TEXT) TO app_service;
