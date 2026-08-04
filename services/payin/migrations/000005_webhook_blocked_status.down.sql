ALTER TABLE payin_webhook_events DROP CONSTRAINT payin_webhook_events_status_check;
ALTER TABLE payin_webhook_events ADD CONSTRAINT payin_webhook_events_status_check
    CHECK (status IN ('received','posted','failed'));
