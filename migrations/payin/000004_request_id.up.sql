-- docs/plan/36 Task T5: persist the originating HTTP/gRPC request_id for
-- end-to-end tracing — nullable, since a vendor-initiated webhook delivery
-- carries whatever request_id the gateway generated for that inbound call,
-- while historical rows predate this column entirely.
ALTER TABLE payin_webhook_events ADD COLUMN request_id TEXT NULL;
ALTER TABLE payin_topup_intents ADD COLUMN request_id TEXT NULL;
