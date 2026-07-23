-- docs/roadmap/archive/36 Task T5: persist the originating HTTP/gRPC request_id and the
-- calling flow (p2p_transfer|topup|payout, docs/roadmap/archive/37) on every screening
-- event for end-to-end tracing and audit.
ALTER TABLE screening_events ADD COLUMN request_id TEXT NULL;
ALTER TABLE screening_events ADD COLUMN flow TEXT NULL;
