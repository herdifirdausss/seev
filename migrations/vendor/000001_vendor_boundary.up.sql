-- VendorService-owned durable boundary records. Raw callback bytes are
-- access-restricted and are never emitted in logs or ordinary audit views.
CREATE TABLE vendor_callback_inbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor TEXT NOT NULL,
    vendor_event_id TEXT NOT NULL,
    external_reference TEXT NOT NULL DEFAULT '',
    amount TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT '',
    normalized_status TEXT NOT NULL DEFAULT '',
    unknown_vendor_status TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ,
    raw_body BYTEA NOT NULL,
    selected_headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_policy TEXT NOT NULL,
    processing_status TEXT NOT NULL DEFAULT 'received',
    attempts INTEGER NOT NULL DEFAULT 0,
    outcome TEXT NOT NULL DEFAULT '',
    owner_request_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vendor, vendor_event_id),
    CHECK (processing_status IN ('received', 'processing', 'finalized', 'ignored', 'unmatched', 'retry', 'dead'))
);
CREATE INDEX vendor_callback_inbox_processing_idx ON vendor_callback_inbox (processing_status, updated_at);
CREATE INDEX vendor_callback_inbox_reference_idx ON vendor_callback_inbox (vendor, external_reference);

CREATE TABLE vendor_outbound_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    flow TEXT NOT NULL,
    vendor TEXT NOT NULL,
    request_id TEXT NOT NULL,
    vendor_reference TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL,
    operation TEXT NOT NULL,
    outcome TEXT NOT NULL,
    sanitized_response JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flow, request_id, attempt, operation)
);
CREATE INDEX vendor_outbound_attempts_request_idx ON vendor_outbound_attempts (flow, request_id, created_at);

-- VendorService writes only its own boundary rows; it never receives a
-- cross-service app role grant to domain tables. The deployment bootstrap
-- creates vendor_app, while lightweight integration databases may intentionally
-- omit login roles; keep schema migration independent of that bootstrap.
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'vendor_app') THEN
        GRANT SELECT, INSERT, UPDATE ON vendor_callback_inbox TO vendor_app;
        GRANT SELECT, INSERT ON vendor_outbound_attempts TO vendor_app;
    END IF;
END;
$$;
