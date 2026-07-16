-- docs/plan/36 Task T5: persist the originating HTTP/gRPC request_id on
-- payout_requests for end-to-end tracing. Also fixes a pre-existing naming
-- collision: payout_vendor_calls.request_id is actually the payout_requests
-- UUID (a foreign key), NOT a trace id — rename it before introducing a
-- REAL trace request_id column anywhere near this table, or the two
-- concepts become impossible to tell apart by name alone.
ALTER TABLE payout_requests ADD COLUMN request_id TEXT NULL;

ALTER TABLE payout_vendor_calls RENAME COLUMN request_id TO payout_request_id;
