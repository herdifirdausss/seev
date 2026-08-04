CREATE OR REPLACE FUNCTION fn_retention_purge_scheduled_transactions(
  p_job_id UUID,p_batch_size INT,p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM scheduled_transactions s
    WHERE s.status IN('finished','failed') AND s.updated_at<now()-INTERVAL '365 days'
      AND NOT fn_ledger_retention_hold_covers('scheduled_transactions',s.user_id::text,s.id::text,s.updated_at);
  ELSE
    WITH eligible AS (
      SELECT s.id FROM scheduled_transactions s
      WHERE s.status IN('finished','failed') AND s.updated_at<now()-INTERVAL '365 days'
        AND NOT fn_ledger_retention_hold_covers('scheduled_transactions',s.user_id::text,s.id::text,s.updated_at)
      ORDER BY s.updated_at,s.id LIMIT p_batch_size FOR UPDATE OF s SKIP LOCKED
    ), deleted AS (
      DELETE FROM scheduled_transactions s USING eligible e WHERE s.id=e.id RETURNING s.id
    ) SELECT count(*) INTO v_affected FROM deleted;
  END IF;
  INSERT INTO ledger_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'ledger.scheduled_transactions','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;
GRANT EXECUTE ON FUNCTION fn_retention_purge_scheduled_transactions(UUID,INT,BOOLEAN) TO app_service;
