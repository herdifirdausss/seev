DROP INDEX IF EXISTS idx_kyc_submissions_rescreen;
ALTER TABLE kyc_submissions
  ADD COLUMN rescreen_name TEXT,
  ADD COLUMN rescreen_birth_date TEXT;
ALTER TABLE kyc_submissions DROP COLUMN rescreen_eligible;
