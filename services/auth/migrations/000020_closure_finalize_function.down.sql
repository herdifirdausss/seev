REVOKE EXECUTE ON FUNCTION public.fn_auth_finalize_credentials(UUID) FROM app_service;
DROP FUNCTION IF EXISTS public.fn_auth_finalize_credentials(UUID);
