-- docs/plan/29 Task T2: database-driven payin routing.
CREATE TABLE payin_vendor_gateways (
    vendor  TEXT PRIMARY KEY,
    gateway TEXT NOT NULL
);

CREATE TABLE payin_routing_rules (
    id         UUID PRIMARY KEY,
    flow       TEXT NOT NULL DEFAULT 'topup' CHECK (flow IN ('topup')),
    priority   INT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    currency   TEXT,
    min_amount BIGINT,
    max_amount BIGINT,
    user_id    UUID,
    vendor     TEXT NOT NULL REFERENCES payin_vendor_gateways(vendor),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flow, priority),
    CHECK (min_amount IS NULL OR min_amount >= 0),
    CHECK (max_amount IS NULL OR max_amount >= 0),
    CHECK (min_amount IS NULL OR max_amount IS NULL OR min_amount <= max_amount)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON payin_vendor_gateways, payin_routing_rules TO app_service;
GRANT SELECT ON payin_vendor_gateways, payin_routing_rules TO app_readonly;

ALTER TABLE payin_vendor_gateways ENABLE ROW LEVEL SECURITY;
ALTER TABLE payin_vendor_gateways FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON payin_vendor_gateways FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON payin_vendor_gateways FOR SELECT TO app_readonly USING (true);

ALTER TABLE payin_routing_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE payin_routing_rules FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_all_service ON payin_routing_rules FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON payin_routing_rules FOR SELECT TO app_readonly USING (true);

INSERT INTO payin_vendor_gateways (vendor, gateway) VALUES ('mockvendor', 'bca');
INSERT INTO payin_routing_rules (id, flow, priority, vendor)
VALUES ('00000000-0000-7000-8000-000000000029', 'topup', 1000, 'mockvendor');
