-- merchant_webhook_deliveries.event_id (FK) had no dedicated index — the
-- only index touching it, idx_merchant_webhook_deliveries_automatic_unique,
-- is partial (WHERE replay_of_delivery_id IS NULL) and leads with
-- endpoint_id, so it can't serve a lookup keyed on event_id alone and
-- excludes replay rows entirely.
-- fn_retention_purge_merchant_webhook_events's NOT EXISTS check against
-- this table (services/gateway/migrations/000005_merchant_retention.up.sql) forces a
-- sequential scan per candidate event without a real index here (schema
-- audit finding).
CREATE INDEX idx_merchant_webhook_deliveries_event ON merchant_webhook_deliveries(event_id);
