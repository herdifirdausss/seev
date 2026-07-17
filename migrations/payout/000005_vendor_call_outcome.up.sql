-- docs/plan/40 Task T3: classify every payout_vendor_calls row into
-- accepted|rejected|uncertain — the SOLE source of truth the anti-double-
-- payout failover rule (mayFailover) reads. Failover to a different vendor
-- is allowed ONLY while no row for a request has ever landed
-- accepted/uncertain; a synchronous business rejection ('rejected') never
-- blocks failover, an infra failure ('uncertain') pins the request to that
-- vendor forever.
ALTER TABLE payout_vendor_calls ADD COLUMN outcome TEXT NOT NULL DEFAULT 'uncertain'
    CHECK (outcome IN ('accepted', 'rejected', 'uncertain'));
ALTER TABLE payout_vendor_calls ALTER COLUMN outcome DROP DEFAULT;
