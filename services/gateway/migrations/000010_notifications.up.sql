-- C3 expands Gateway's existing inbox without replacing it. Every change is
-- additive so an old Gateway binary can continue to list/read legacy rows
-- while the planner is rolled out.
ALTER TABLE notif_notifications
    ADD COLUMN IF NOT EXISTS event_type TEXT NOT NULL DEFAULT 'ledger.transaction.posted.v1',
    ADD COLUMN IF NOT EXISTS source_service TEXT NOT NULL DEFAULT 'ledger',
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'legacy',
    ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'money_movement',
    ADD COLUMN IF NOT EXISTS priority TEXT NOT NULL DEFAULT 'high',
    ADD COLUMN IF NOT EXISTS requirement TEXT NOT NULL DEFAULT 'transactional',
    ADD COLUMN IF NOT EXISTS locale TEXT NOT NULL DEFAULT 'en-US',
    ADD COLUMN IF NOT EXISTS template_version_id UUID,
    ADD COLUMN IF NOT EXISTS deep_link TEXT,
    ADD COLUMN IF NOT EXISTS context JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS content_hash BYTEA,
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS idx_notif_notifications_user_keyset
    ON notif_notifications(user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_notif_notifications_unread
    ON notif_notifications(user_id, created_at DESC, id DESC) WHERE read_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_notif_notifications_kind
    ON notif_notifications(user_id, kind, created_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_notif_notifications_event_user_kind
    ON notif_notifications(event_id, user_id, kind);

CREATE TABLE IF NOT EXISTS notif_event_inbox (
    id             UUID PRIMARY KEY,
    source_service TEXT NOT NULL,
    event_id       UUID NOT NULL,
    event_type     TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    payload_hash   BYTEA NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('received', 'processed', 'failed')),
    error_code     TEXT,
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_service, event_id)
);
CREATE INDEX IF NOT EXISTS idx_notif_event_inbox_pending
    ON notif_event_inbox(status, received_at);

CREATE TABLE IF NOT EXISTS notif_templates (
    id              UUID PRIMARY KEY,
    kind            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL,
    variable_schema JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notif_template_versions (
    id                  UUID PRIMARY KEY,
    template_id         UUID NOT NULL REFERENCES notif_templates(id),
    channel             TEXT NOT NULL CHECK (channel IN ('in_app', 'email', 'push')),
    locale              TEXT NOT NULL,
    version             INTEGER NOT NULL CHECK (version > 0),
    status              TEXT NOT NULL CHECK (status IN ('draft', 'pending_approval', 'active', 'retired', 'rejected')),
    subject_template    TEXT,
    title_template      TEXT,
    body_text_template  TEXT NOT NULL,
    body_html_template  TEXT,
    content_hash        BYTEA NOT NULL,
    created_by          TEXT NOT NULL,
    submitted_by        TEXT,
    approved_by         TEXT,
    rejected_by         TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at        TIMESTAMPTZ,
    published_at        TIMESTAMPTZ,
    retired_at          TIMESTAMPTZ,
    rejection_reason    TEXT,
    UNIQUE (template_id, channel, locale, version),
    CHECK (approved_by IS NULL OR approved_by <> created_by)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_notif_template_versions_active
    ON notif_template_versions(template_id, channel, locale) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_notif_template_versions_lookup
    ON notif_template_versions(template_id, channel, locale, status, version DESC);

-- The initial registry has an active, reviewable v1 baseline. Runtime code
-- still has a deterministic built-in fallback so a schema-only expand release
-- cannot make mandatory in-app delivery disappear.
INSERT INTO notif_templates(id, kind, description, variable_schema) VALUES
 ('10000000-0000-0000-0000-000000000001','money.transfer.sent','Transfer sent','{"version":1,"variables":["amount","transaction","action"]}'),
 ('10000000-0000-0000-0000-000000000002','money.transfer.received','Transfer received','{"version":1,"variables":["amount","transaction","action"]}'),
 ('10000000-0000-0000-0000-000000000003','money.topup.succeeded','Top-up succeeded','{"version":1,"variables":["amount","transaction","action"]}'),
 ('10000000-0000-0000-0000-000000000004','money.payout.succeeded','Payout succeeded','{"version":1,"variables":["amount","transaction","action"]}'),
 ('10000000-0000-0000-0000-000000000005','money.payout.cancelled','Payout cancelled','{"version":1,"variables":["amount","transaction","action"]}'),
 ('10000000-0000-0000-0000-000000000006','system.daily_digest','Daily notification digest','{"version":1,"variables":["window_date","items","more_count"]}')
ON CONFLICT (kind) DO NOTHING;

INSERT INTO notif_template_versions(id,template_id,channel,locale,version,status,subject_template,title_template,body_text_template,body_html_template,content_hash,created_by,published_at)
VALUES
 ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','in_app','en-US',1,'active',NULL,'Transfer sent','Your {{.Amount.Display}} transfer was sent successfully.',NULL,decode(md5('transfer-sent-in-app-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','email','en-US',1,'active','Your transfer was sent', 'Transfer sent','Your {{.Amount.Display}} transfer was sent successfully.','<p>Your {{.Amount.Display}} transfer was sent successfully.</p>',decode(md5('transfer-sent-email-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','push','en-US',1,'active',NULL,'Transaction update','Open Seev to view the details.',NULL,decode(md5('transfer-sent-push-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000002','in_app','en-US',1,'active',NULL,'Transfer received','You received {{.Amount.Display}}.',NULL,decode(md5('transfer-received-in-app-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000002','email','en-US',1,'active','You received a transfer','Transfer received','You received {{.Amount.Display}}.','<p>You received {{.Amount.Display}}.</p>',decode(md5('transfer-received-email-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000002','push','en-US',1,'active',NULL,'Transaction update','Open Seev to view the details.',NULL,decode(md5('transfer-received-push-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000007','10000000-0000-0000-0000-000000000003','in_app','en-US',1,'active',NULL,'Funds received','Your {{.Amount.Display}} top-up was successful and your balance increased.',NULL,decode(md5('topup-in-app-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000008','10000000-0000-0000-0000-000000000003','email','en-US',1,'active','Your top-up was successful','Funds received','Your {{.Amount.Display}} top-up was successful and your balance increased.','<p>Your {{.Amount.Display}} top-up was successful and your balance increased.</p>',decode(md5('topup-email-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000009','10000000-0000-0000-0000-000000000003','push','en-US',1,'active',NULL,'Transaction update','Open Seev to view the details.',NULL,decode(md5('topup-push-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000010','10000000-0000-0000-0000-000000000004','in_app','en-US',1,'active',NULL,'Withdrawal successful','Your {{.Amount.Display}} withdrawal was processed successfully.',NULL,decode(md5('payout-success-in-app-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000011','10000000-0000-0000-0000-000000000004','email','en-US',1,'active','Your withdrawal was successful','Withdrawal successful','Your {{.Amount.Display}} withdrawal was processed successfully.','<p>Your {{.Amount.Display}} withdrawal was processed successfully.</p>',decode(md5('payout-success-email-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000012','10000000-0000-0000-0000-000000000004','push','en-US',1,'active',NULL,'Transaction update','Open Seev to view the details.',NULL,decode(md5('payout-success-push-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000013','10000000-0000-0000-0000-000000000005','in_app','en-US',1,'active',NULL,'Withdrawal canceled','Your {{.Amount.Display}} withdrawal was canceled and the funds were returned.',NULL,decode(md5('payout-cancel-in-app-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000014','10000000-0000-0000-0000-000000000005','email','en-US',1,'active','Your withdrawal was canceled','Withdrawal canceled','Your {{.Amount.Display}} withdrawal was canceled and the funds were returned.','<p>Your {{.Amount.Display}} withdrawal was canceled and the funds were returned.</p>',decode(md5('payout-cancel-email-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000015','10000000-0000-0000-0000-000000000005','push','en-US',1,'active',NULL,'Transaction update','Open Seev to view the details.',NULL,decode(md5('payout-cancel-push-v1'),'hex'),'system',now()),
 ('20000000-0000-0000-0000-000000000016','10000000-0000-0000-0000-000000000006','email','en-US',1,'active','Your Seev daily notification digest','Your Seev daily notification digest','{{range .Items}}- {{.Title}}: {{.Body}}\n{{end}}{{if .MoreCount}}\nAnd {{.MoreCount}} more notification(s).\n{{end}}','<h1>Your Seev daily notification digest</h1><ul>{{range .Items}}<li><strong>{{.Title}}</strong>: {{.Body}}</li>{{end}}</ul>{{if .MoreCount}}<p>And {{.MoreCount}} more notification(s).</p>{{end}}',decode(md5('daily-digest-email-v1'),'hex'),'system',now())
ON CONFLICT (template_id,channel,locale,version) DO NOTHING;

CREATE TABLE IF NOT EXISTS notif_user_settings (
    user_id              UUID PRIMARY KEY,
    locale               TEXT NOT NULL DEFAULT 'en-US',
    timezone             TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    quiet_hours_enabled  BOOLEAN NOT NULL DEFAULT false,
    quiet_hours_start    TIME,
    quiet_hours_end      TIME,
    daily_digest_hour    TIME NOT NULL DEFAULT '08:00',
    version              BIGINT NOT NULL DEFAULT 1,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((quiet_hours_enabled = false) OR (quiet_hours_start IS NOT NULL AND quiet_hours_end IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS notif_preferences (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL,
    category    TEXT NOT NULL,
    channel     TEXT NOT NULL CHECK (channel IN ('in_app', 'email', 'push')),
    mode        TEXT NOT NULL CHECK (mode IN ('immediate', 'daily_digest', 'disabled')),
    version     BIGINT NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, category, channel)
);
CREATE INDEX IF NOT EXISTS idx_notif_preferences_user ON notif_preferences(user_id, category, channel);

CREATE TABLE IF NOT EXISTS notif_device_endpoints (
    id                    UUID PRIMARY KEY,
    user_id               UUID NOT NULL,
    platform              TEXT NOT NULL CHECK (platform IN ('android', 'ios', 'web', 'test')),
    device_name           TEXT,
    token_ciphertext      BYTEA NOT NULL,
    token_key_version     INTEGER NOT NULL,
    token_fingerprint     BYTEA NOT NULL,
    token_suffix          TEXT,
    status                TEXT NOT NULL CHECK (status IN ('active', 'invalid', 'revoked')),
    last_success_at       TIMESTAMPTZ,
    last_failure_at       TIMESTAMPTZ,
    last_failure_code     TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at            TIMESTAMPTZ,
    UNIQUE (user_id, token_fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_notif_devices_active ON notif_device_endpoints(user_id, status) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_notif_devices_fingerprint ON notif_device_endpoints(token_fingerprint);

CREATE TABLE IF NOT EXISTS notif_digest_windows (
    id                 UUID PRIMARY KEY,
    user_id            UUID NOT NULL,
    channel            TEXT NOT NULL CHECK (channel = 'email'),
    locale             TEXT NOT NULL DEFAULT 'en-US',
    timezone           TEXT NOT NULL,
    local_window_date  DATE NOT NULL,
    window_start_at    TIMESTAMPTZ NOT NULL,
    window_end_at      TIMESTAMPTZ NOT NULL,
    scheduled_at       TIMESTAMPTZ NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('scheduled', 'processing', 'delivered', 'suppressed', 'dead')),
    item_count         INTEGER NOT NULL DEFAULT 0,
    delivery_id        UUID,
    lease_owner        TEXT,
    lease_expires_at   TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, channel, local_window_date, timezone)
);
CREATE INDEX IF NOT EXISTS idx_notif_digest_windows_due ON notif_digest_windows(status, scheduled_at);

CREATE TABLE IF NOT EXISTS notif_deliveries (
    id                    UUID PRIMARY KEY,
    notification_id       UUID,
    digest_window_id      UUID,
    user_id               UUID NOT NULL,
    channel               TEXT NOT NULL CHECK (channel IN ('in_app', 'email', 'push')),
    endpoint_id           UUID,
    endpoint_identity     TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL CHECK (status IN ('pending_recipient', 'scheduled', 'processing', 'retry_wait', 'delivered', 'suppressed', 'blocked', 'dead', 'cancelled')),
    template_version_id   UUID NOT NULL,
    locale                TEXT NOT NULL,
    recipient_ciphertext  BYTEA,
    recipient_key_version INTEGER,
    recipient_fingerprint BYTEA,
    rendered_subject      TEXT,
    rendered_title        TEXT,
    rendered_text         TEXT NOT NULL,
    rendered_html         TEXT,
    provider_payload      JSONB,
    content_hash          BYTEA NOT NULL,
    attempt_count         INTEGER NOT NULL DEFAULT 0,
    next_attempt_at       TIMESTAMPTZ,
    lease_owner           TEXT,
    lease_expires_at      TIMESTAMPTZ,
    provider_message_id   TEXT,
    last_error_code       TEXT,
    delivered_at          TIMESTAMPTZ,
    suppressed_at         TIMESTAMPTZ,
    dead_at               TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((notification_id IS NOT NULL) <> (digest_window_id IS NOT NULL)),
    UNIQUE (notification_id, channel, endpoint_identity)
);
CREATE INDEX IF NOT EXISTS idx_notif_deliveries_due
    ON notif_deliveries(channel, status, next_attempt_at, created_at);
CREATE INDEX IF NOT EXISTS idx_notif_deliveries_leases
    ON notif_deliveries(channel, lease_expires_at) WHERE status = 'processing';
CREATE INDEX IF NOT EXISTS idx_notif_deliveries_admin
    ON notif_deliveries(status, channel, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_notif_deliveries_digest
    ON notif_deliveries(digest_window_id, channel) WHERE digest_window_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS notif_delivery_attempts (
    id                   UUID PRIMARY KEY,
    delivery_id          UUID NOT NULL REFERENCES notif_deliveries(id),
    attempt_number       INTEGER NOT NULL,
    lease_owner          TEXT NOT NULL,
    provider             TEXT NOT NULL,
    started_at           TIMESTAMPTZ NOT NULL,
    finished_at          TIMESTAMPTZ,
    result               TEXT NOT NULL,
    status_class         TEXT,
    provider_message_id  TEXT,
    error_code           TEXT,
    duration_ms          INTEGER,
    response_excerpt     TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (delivery_id, attempt_number)
);

CREATE TABLE IF NOT EXISTS notif_digest_items (
    digest_window_id UUID NOT NULL REFERENCES notif_digest_windows(id),
    notification_id  UUID NOT NULL REFERENCES notif_notifications(id) ON DELETE CASCADE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (digest_window_id, notification_id)
);

CREATE TABLE IF NOT EXISTS notif_channel_controls (
    channel     TEXT PRIMARY KEY CHECK (channel IN ('email', 'push', 'digest')),
    state       TEXT NOT NULL CHECK (state IN ('running', 'paused', 'drain_only')),
    reason      TEXT,
    changed_by  TEXT NOT NULL,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ,
    version     BIGINT NOT NULL DEFAULT 1
);
INSERT INTO notif_channel_controls(channel, state, changed_by)
VALUES ('email', 'running', 'system'), ('push', 'running', 'system'), ('digest', 'running', 'system')
ON CONFLICT (channel) DO NOTHING;

GRANT SELECT, INSERT, UPDATE ON notif_event_inbox, notif_templates, notif_template_versions,
    notif_user_settings, notif_preferences, notif_device_endpoints, notif_digest_windows,
    notif_deliveries, notif_delivery_attempts, notif_digest_items, notif_channel_controls TO app_service;
-- Privacy closure removes account-scoped settings, preferences, and revoked
-- device credentials. Delivery evidence and notification history remain
-- update-only and are pseudonymized instead.
GRANT DELETE ON notif_user_settings, notif_preferences, notif_device_endpoints TO app_service;
GRANT SELECT ON notif_event_inbox, notif_templates, notif_template_versions, notif_user_settings,
    notif_preferences, notif_device_endpoints, notif_digest_windows, notif_deliveries,
    notif_delivery_attempts, notif_digest_items, notif_channel_controls TO app_readonly;

DO $$
DECLARE table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['notif_event_inbox','notif_templates','notif_template_versions',
        'notif_user_settings','notif_preferences','notif_device_endpoints','notif_digest_windows',
        'notif_deliveries','notif_delivery_attempts','notif_digest_items','notif_channel_controls']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY pol_c3_service_%I ON %I FOR ALL TO app_service USING (true) WITH CHECK (true)', table_name, table_name);
        EXECUTE format('CREATE POLICY pol_c3_readonly_%I ON %I FOR SELECT TO app_readonly USING (true)', table_name, table_name);
    END LOOP;
END $$;
