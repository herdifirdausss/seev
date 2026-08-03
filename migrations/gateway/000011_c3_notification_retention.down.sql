DROP FUNCTION IF EXISTS fn_retention_purge_notification_digest_windows(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_notification_digest_items(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_notification_deliveries(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_redact_notification_device_tokens(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_redact_notification_recipient(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_notification_delivery_attempts(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_notification_event_inbox_failed(UUID, INT, BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_notification_event_inbox_processed(UUID, INT, BOOLEAN);

-- The forward migration intentionally redacts old invalid/revoked tokens.
-- A rollback cannot reconstruct those credentials, so it must retain the
-- nullable column rather than fail while trying to restore NOT NULL.
