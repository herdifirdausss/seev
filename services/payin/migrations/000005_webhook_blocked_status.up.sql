-- docs/roadmap/archive/37 Task T4: fraud screening moved pre-posting (before
-- poster.Post) — a Block verdict marks the webhook event 'blocked' rather
-- than reusing 'failed', so an operator reviewing payin_webhook_events can
-- tell "fraud rejected this deposit" apart from "the ledger post itself
-- failed" (suspended account, closed account, etc.) at a glance.
ALTER TABLE payin_webhook_events DROP CONSTRAINT payin_webhook_events_status_check;
ALTER TABLE payin_webhook_events ADD CONSTRAINT payin_webhook_events_status_check
    CHECK (status IN ('received','posted','failed','blocked'));
