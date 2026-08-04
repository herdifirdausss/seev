DROP FUNCTION IF EXISTS fn_retention_purge_kyc_submissions(UUID,INT,BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_kyc_documents(UUID,INT,BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_kyc_apply_retries_dead(UUID,INT,BOOLEAN);
DROP TABLE IF EXISTS auth_kyc_retry_summaries;
