DROP FUNCTION IF EXISTS fn_retention_purge_merchant_webhook_events(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_merchant_webhook_deliveries(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_merchant_event_inbox(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_merchant_api_keys_revoked(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_merchant_idempotency_records(UUID, INT, BOOLEAN);
