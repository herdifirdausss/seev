-- docs/plan/38 Task T5: a payout created with a fee quote stores the
-- QUOTED fee at create time — settle (possibly hours later, via the resume
-- job) uses this stored value instead of re-resolving fee_rules, so an
-- admin changing pricing in between never changes what the user was quoted.
ALTER TABLE payout_requests ADD COLUMN fee_quote_id UUID;
ALTER TABLE payout_requests ADD COLUMN fee_amount BIGINT;
ALTER TABLE payout_requests ADD COLUMN fee_gateway TEXT;

-- 'rejected' is the terminal status for a quote consumption failure
-- (expired/mismatch) at Create — no hold was ever posted, this row exists
-- purely as a record of the rejected attempt (Postgres requires
-- DROP+ADD to extend a CHECK, cannot ALTER in place).
ALTER TABLE payout_requests DROP CONSTRAINT payout_requests_status_check;
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_status_check
    CHECK (status IN ('created','held','submitted','vendor_pending','settled','failed','cancelled','rejected'));
