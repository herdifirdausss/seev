-- Plan 51 K10 / world-class plan 8.6: credential deletion is a privileged
-- maintenance operation. The runtime app role must not receive table DELETE;
-- it may invoke this one narrowly scoped function from the closure worker.
--
-- Keep the function's search path fixed and qualify the table name. PUBLIC
-- must not retain the default function EXECUTE privilege, otherwise the
-- SECURITY DEFINER boundary would be equivalent to an unrestricted delete.
REVOKE DELETE ON auth_credentials FROM app_service;

CREATE FUNCTION public.fn_auth_finalize_credentials(p_user_id UUID)
RETURNS INTEGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_deleted INTEGER;
BEGIN
    DELETE FROM public.auth_credentials c
    WHERE c.user_id = p_user_id
      AND EXISTS (
          SELECT 1
          FROM public.auth_users u
          WHERE u.id = p_user_id
            AND u.status IN ('closing', 'closed')
      );
    GET DIAGNOSTICS v_deleted = ROW_COUNT;
    RETURN v_deleted;
END;
$$;

REVOKE ALL ON FUNCTION public.fn_auth_finalize_credentials(UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.fn_auth_finalize_credentials(UUID) TO app_service;
