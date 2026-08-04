CREATE OR REPLACE FUNCTION fn_retention_purge_intake_commands(
  p_job_id UUID,p_batch_size INT,p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM payin_intake_commands
    WHERE applied AND created_at<now()-INTERVAL '365 days';
  ELSE
    WITH eligible AS (
      SELECT command_id FROM payin_intake_commands
      WHERE applied AND created_at<now()-INTERVAL '365 days'
      ORDER BY created_at,command_id LIMIT p_batch_size FOR UPDATE SKIP LOCKED
    ), deleted AS (
      DELETE FROM payin_intake_commands c USING eligible e WHERE c.command_id=e.command_id RETURNING c.command_id
    ) SELECT count(*) INTO v_affected FROM deleted;
  END IF;
  INSERT INTO payin_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'payin.intake_commands','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;
GRANT EXECUTE ON FUNCTION fn_retention_purge_intake_commands(UUID,INT,BOOLEAN) TO app_service;
