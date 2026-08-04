ALTER TABLE payout_requests
    DROP COLUMN IF EXISTS destination_ciphertext,
    DROP COLUMN IF EXISTS destination_key_version;
