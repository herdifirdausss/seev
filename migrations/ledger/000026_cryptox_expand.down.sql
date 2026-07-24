ALTER TABLE recon_batches
    DROP COLUMN IF EXISTS source_filename_ciphertext,
    DROP COLUMN IF EXISTS source_filename_key_version;
ALTER TABLE recon_items
    DROP COLUMN IF EXISTS raw_ciphertext,
    DROP COLUMN IF EXISTS raw_key_version;
