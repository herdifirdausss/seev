-- docs/plan/25 Task T4: in-app notification inbox — the first RabbitMQ
-- consumer in this codebase. internal/notify subscribes to
-- events.TypeTransactionPosted (declared queue ledger.events.notifications)
-- and writes one row per (event_id, user_id) so a two-party transaction
-- (transfer_p2p) produces two independent, independently-readable rows.
--
-- UNIQUE(event_id, user_id) is the at-least-once dedup guard: RabbitMQ
-- delivery is at-least-once (docs/events.md), so the consumer's INSERT is
-- always `... ON CONFLICT (event_id, user_id) DO NOTHING` — redelivery of
-- the same outbox event never produces a second row for the same user.
CREATE TABLE notif_notifications (
    id         UUID        PRIMARY KEY,
    user_id    UUID        NOT NULL,
    event_id   UUID        NOT NULL,       -- outbox_events.id (the RabbitMQ message_id)
    type       TEXT        NOT NULL,       -- transaction_type from TransactionPosted (money_in, transfer_p2p, ...)
    title      TEXT        NOT NULL,
    body       TEXT        NOT NULL,
    payload    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    read_at    TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (event_id, user_id)
);

-- Keyset pagination for GET /api/v1/notifications?limit=&before= — own
-- rows only, newest first.
CREATE INDEX idx_notif_notifications_user ON notif_notifications(user_id, created_at DESC);

-- app_service: SELECT+INSERT (consumer writes), UPDATE (mark-read). Never
-- DELETE — same immutable-except-status-column philosophy as
-- payin_webhook_events/payin_topup_intents.
GRANT SELECT, INSERT, UPDATE ON notif_notifications TO app_service;
GRANT SELECT ON notif_notifications TO app_readonly;

ALTER TABLE notif_notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notif_notifications FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service   ON notif_notifications FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON notif_notifications FOR SELECT TO app_readonly USING (true);
