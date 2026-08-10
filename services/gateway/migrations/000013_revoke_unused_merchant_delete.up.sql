-- Runtime repositories never directly delete these child/evidence rows.
-- Lifecycle removal remains available only through the SECURITY DEFINER
-- retention functions introduced in 000005.
REVOKE DELETE ON
    merchant_api_key_scopes,
    merchant_webhook_attempts,
    merchant_webhook_events
FROM app_service;
