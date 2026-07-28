-- VendorService delivers normalized callbacks without an authoritative Seev user id.
ALTER TABLE payin_webhook_events
    ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE payin_webhook_events DROP CONSTRAINT payin_webhook_events_status_check;
ALTER TABLE payin_webhook_events ADD CONSTRAINT payin_webhook_events_status_check
    CHECK (status IN ('received','posted','failed','blocked','ignored'));
