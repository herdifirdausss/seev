DROP TRIGGER IF EXISTS trg_kyc_decision_authorization ON kyc_submissions;
DROP FUNCTION IF EXISTS fn_enforce_kyc_decision_authorization();
DROP TRIGGER IF EXISTS trg_kyc_decision_request_state ON kyc_decision_requests;
DROP FUNCTION IF EXISTS fn_enforce_kyc_decision_request_state();

ALTER TABLE kyc_apply_retries DROP COLUMN IF EXISTS decision_request_id;
ALTER TABLE kyc_submissions DROP COLUMN IF EXISTS decision_request_id;
DROP TABLE IF EXISTS kyc_decision_requests;
