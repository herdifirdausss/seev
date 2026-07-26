CREATE TABLE auth_kyc_retry_summaries (
  retry_id UUID PRIMARY KEY,
  retry_count INT NOT NULL,
  error_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT,INSERT ON auth_kyc_retry_summaries TO app_service;
GRANT SELECT ON auth_kyc_retry_summaries TO app_readonly;
ALTER TABLE auth_kyc_retry_summaries ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_kyc_retry_summaries FORCE ROW LEVEL SECURITY;
CREATE POLICY auth_kyc_retry_summaries_service ON auth_kyc_retry_summaries FOR ALL TO app_service USING(true) WITH CHECK(true);
CREATE POLICY auth_kyc_retry_summaries_readonly ON auth_kyc_retry_summaries FOR SELECT TO app_readonly USING(true);

CREATE OR REPLACE FUNCTION fn_retention_purge_kyc_apply_retries_dead(
  p_job_id UUID,p_batch_size INT,p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM kyc_apply_retries r
    WHERE r.status='dead' AND r.updated_at<now()-INTERVAL '365 days'
      AND NOT fn_auth_retention_hold_covers('kyc_apply_retries',r.user_id::text,r.id::text,r.updated_at);
  ELSE
    WITH candidates AS MATERIALIZED (
      SELECT r.id,r.user_id,r.submission_id,r.retry_count,r.last_error
      FROM kyc_apply_retries r
      WHERE r.status='dead' AND r.updated_at<now()-INTERVAL '365 days'
        AND NOT fn_auth_retention_hold_covers('kyc_apply_retries',r.user_id::text,r.id::text,r.updated_at)
      ORDER BY r.updated_at,r.id LIMIT p_batch_size FOR UPDATE OF r SKIP LOCKED
    ), summarized AS (
      INSERT INTO auth_kyc_retry_summaries(retry_id,retry_count,error_hash)
      SELECT id,retry_count,encode(digest(convert_to(COALESCE(last_error,''),'UTF8'),'sha256'),'hex')
      FROM candidates ON CONFLICT(retry_id) DO NOTHING RETURNING retry_id
    ), deleted AS (
      DELETE FROM kyc_apply_retries r USING candidates c
      WHERE r.id=c.id AND EXISTS(SELECT 1 FROM auth_kyc_retry_summaries s WHERE s.retry_id=r.id)
      RETURNING r.id
    ) SELECT count(*) INTO v_affected FROM deleted;
  END IF;
  INSERT INTO auth_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'auth.kyc_apply_retries.dead','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;

-- First persist object delete intents. Metadata is removed only on a later
-- run after the object worker has confirmed deletion by setting deleted_at.
CREATE OR REPLACE FUNCTION fn_retention_purge_kyc_documents(
  p_job_id UUID,p_batch_size INT,p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_enqueued INT:=0; v_deleted INT:=0; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM kyc_documents d
    WHERE EXISTS(SELECT 1 FROM privacy_requests p WHERE p.user_id=d.user_id AND p.request_type='closure'
      AND p.status='completed' AND p.ready_at<now()-INTERVAL '365 days')
      AND NOT fn_auth_retention_hold_covers('kyc_documents',d.user_id::text,d.id::text,d.created_at);
  ELSE
    WITH eligible AS (
      SELECT d.id,d.object_key FROM kyc_documents d
      WHERE d.deleted_at IS NULL
        AND EXISTS(SELECT 1 FROM privacy_requests p WHERE p.user_id=d.user_id AND p.request_type='closure'
          AND p.status='completed' AND p.ready_at<now()-INTERVAL '365 days')
        AND NOT fn_auth_retention_hold_covers('kyc_documents',d.user_id::text,d.id::text,d.created_at)
      ORDER BY d.created_at,d.id LIMIT p_batch_size FOR UPDATE OF d SKIP LOCKED
    ), inserted AS (
      INSERT INTO auth_object_delete_outbox(id,ref_table,ref_id,object_key)
      SELECT gen_random_uuid(),'kyc_documents',id,object_key FROM eligible
      ON CONFLICT(ref_table,ref_id) DO NOTHING RETURNING id
    ) SELECT count(*) INTO v_enqueued FROM inserted;

    WITH eligible AS (
      SELECT d.id FROM kyc_documents d
      WHERE d.deleted_at IS NOT NULL
        AND EXISTS(SELECT 1 FROM privacy_requests p WHERE p.user_id=d.user_id AND p.request_type='closure'
          AND p.status='completed' AND p.ready_at<now()-INTERVAL '365 days')
        AND NOT fn_auth_retention_hold_covers('kyc_documents',d.user_id::text,d.id::text,d.created_at)
      ORDER BY d.created_at,d.id LIMIT p_batch_size FOR UPDATE OF d SKIP LOCKED
    ), deleted AS (
      DELETE FROM kyc_documents d USING eligible e WHERE d.id=e.id RETURNING d.id
    ) SELECT count(*) INTO v_deleted FROM deleted;
    v_affected:=v_enqueued+v_deleted;
  END IF;
  INSERT INTO auth_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'auth.kyc_documents','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;

CREATE OR REPLACE FUNCTION fn_retention_purge_kyc_submissions(
  p_job_id UUID,p_batch_size INT,p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM kyc_submissions s
    WHERE s.status IN('approved','rejected')
      AND EXISTS(SELECT 1 FROM privacy_requests p WHERE p.user_id=s.user_id AND p.request_type='closure'
        AND p.status='completed' AND p.ready_at<now()-INTERVAL '365 days')
      AND NOT EXISTS(SELECT 1 FROM kyc_documents d WHERE d.submission_id=s.id)
      AND NOT EXISTS(SELECT 1 FROM kyc_apply_retries r WHERE r.submission_id=s.id)
      AND NOT fn_auth_retention_hold_covers('kyc_submissions',s.user_id::text,s.id::text,s.created_at);
  ELSE
    WITH eligible AS (
      SELECT s.id FROM kyc_submissions s
      WHERE s.status IN('approved','rejected')
        AND EXISTS(SELECT 1 FROM privacy_requests p WHERE p.user_id=s.user_id AND p.request_type='closure'
          AND p.status='completed' AND p.ready_at<now()-INTERVAL '365 days')
        AND NOT EXISTS(SELECT 1 FROM kyc_documents d WHERE d.submission_id=s.id)
        AND NOT EXISTS(SELECT 1 FROM kyc_apply_retries r WHERE r.submission_id=s.id)
        AND NOT fn_auth_retention_hold_covers('kyc_submissions',s.user_id::text,s.id::text,s.created_at)
      ORDER BY s.decided_at,s.id LIMIT p_batch_size FOR UPDATE OF s SKIP LOCKED
    ), deleted AS (
      DELETE FROM kyc_submissions s USING eligible e WHERE s.id=e.id RETURNING s.id
    ) SELECT count(*) INTO v_affected FROM deleted;
  END IF;
  INSERT INTO auth_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'auth.kyc_submissions','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_kyc_apply_retries_dead(UUID,INT,BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_kyc_documents(UUID,INT,BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_kyc_submissions(UUID,INT,BOOLEAN) TO app_service;
