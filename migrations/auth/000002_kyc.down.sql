-- Reverse of 000002_kyc: submissions first because it references auth_users.
DROP TABLE IF EXISTS kyc_submissions;
ALTER TABLE auth_users DROP COLUMN IF EXISTS kyc_level;
