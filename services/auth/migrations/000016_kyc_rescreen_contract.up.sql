-- Remove the last plaintext KYC projections. The boolean preserves an
-- indexed/filterable eligibility hint without retaining name or birth date.
ALTER TABLE kyc_submissions ADD COLUMN rescreen_eligible BOOLEAN NOT NULL DEFAULT false;
UPDATE kyc_submissions SET rescreen_eligible = (rescreen_name IS NOT NULL);
ALTER TABLE kyc_submissions
  DROP COLUMN rescreen_name,
  DROP COLUMN rescreen_birth_date;
CREATE INDEX idx_kyc_submissions_rescreen
  ON kyc_submissions(user_id,decided_at DESC,created_at DESC,id DESC)
  WHERE status='approved' AND rescreen_eligible;
