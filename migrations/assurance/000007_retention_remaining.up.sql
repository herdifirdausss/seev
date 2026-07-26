CREATE TABLE assurance_incident_summaries (
    -- Deliberately not a foreign key: this is the durable successor record
    -- that must survive deletion of the failed assurance_runs row.
    run_id UUID PRIMARY KEY,
    error_code TEXT NOT NULL,
    summary_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT ON assurance_incident_summaries TO app_service;
GRANT SELECT ON assurance_incident_summaries TO app_readonly;
ALTER TABLE assurance_incident_summaries ENABLE ROW LEVEL SECURITY;
ALTER TABLE assurance_incident_summaries FORCE ROW LEVEL SECURITY;
CREATE POLICY assurance_incident_summaries_service ON assurance_incident_summaries FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY assurance_incident_summaries_readonly ON assurance_incident_summaries FOR SELECT TO app_readonly USING (true);

CREATE OR REPLACE FUNCTION fn_assurance_summarize_failed_runs(p_batch_size INT) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT;
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  WITH eligible AS (
    SELECT r.id,r.error_code,encode(digest(
      convert_to(r.error_code,'UTF8')||decode('00','hex')||convert_to(r.error_message,'UTF8'),
      'sha256'
    ),'hex') summary_hash
    FROM assurance_runs r WHERE r.status='failed' AND r.finished_at IS NOT NULL
      AND NOT EXISTS(SELECT 1 FROM assurance_incident_summaries s WHERE s.run_id=r.id)
    ORDER BY r.finished_at,r.id LIMIT p_batch_size FOR UPDATE OF r SKIP LOCKED
  ), inserted AS (
    INSERT INTO assurance_incident_summaries(run_id,error_code,summary_hash)
    SELECT id,error_code,summary_hash FROM eligible ON CONFLICT DO NOTHING RETURNING run_id
  ) SELECT count(*) INTO v_affected FROM inserted;
  RETURN v_affected;
END $$;

CREATE OR REPLACE FUNCTION fn_retention_purge_runs_failed(p_job_id UUID,p_batch_size INT,p_dry_run BOOLEAN) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM assurance_runs r WHERE r.status='failed'
      AND r.finished_at IS NOT NULL AND r.finished_at<now()-INTERVAL '180 days';
  ELSE
    WITH candidates AS (
      SELECT r.id,r.error_code,r.error_message,r.finished_at
      FROM assurance_runs r
      WHERE r.status='failed' AND r.finished_at IS NOT NULL
        AND r.finished_at<now()-INTERVAL '180 days'
      ORDER BY r.finished_at,r.id LIMIT p_batch_size
      FOR UPDATE OF r SKIP LOCKED
    ), summarized AS (
      INSERT INTO assurance_incident_summaries(run_id,error_code,summary_hash)
      SELECT c.id,c.error_code,encode(digest(
        convert_to(c.error_code,'UTF8')||decode('00','hex')||convert_to(c.error_message,'UTF8'),
        'sha256'
      ),'hex')
      FROM candidates c ON CONFLICT (run_id) DO NOTHING
      RETURNING run_id
    ), eligible AS (
      SELECT r.id FROM assurance_runs r WHERE r.status='failed' AND r.finished_at<now()-INTERVAL '180 days'
        AND EXISTS(SELECT 1 FROM assurance_incident_summaries s WHERE s.run_id=r.id)
        AND EXISTS(SELECT 1 FROM candidates c WHERE c.id=r.id)
    ), deleted AS (DELETE FROM assurance_runs r USING eligible WHERE r.id=eligible.id RETURNING r.id)
    SELECT count(*) INTO v_affected FROM deleted;
  END IF;
  INSERT INTO assurance_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'assurance.runs.failed','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;

CREATE OR REPLACE FUNCTION fn_retention_purge_findings_resolved(p_job_id UUID,p_batch_size INT,p_dry_run BOOLEAN) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM assurance_findings f WHERE f.status='resolved'
      AND f.resolved_at<now()-INTERVAL '365 days'
      AND NOT EXISTS(SELECT 1 FROM assurance_alert_deliveries d WHERE d.finding_id=f.id)
      AND NOT fn_assurance_retention_hold_covers('assurance_findings',NULL,f.id::text,f.resolved_at);
  ELSE
    WITH eligible AS (
      SELECT f.id FROM assurance_findings f WHERE f.status='resolved' AND f.resolved_at<now()-INTERVAL '365 days'
        AND NOT EXISTS(SELECT 1 FROM assurance_alert_deliveries d WHERE d.finding_id=f.id)
        AND NOT fn_assurance_retention_hold_covers('assurance_findings',NULL,f.id::text,f.resolved_at)
      ORDER BY f.resolved_at,f.id LIMIT p_batch_size FOR UPDATE OF f SKIP LOCKED
    ), deleted AS (DELETE FROM assurance_findings f USING eligible WHERE f.id=eligible.id RETURNING f.id)
    SELECT count(*) INTO v_affected FROM deleted;
  END IF;
  INSERT INTO assurance_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'assurance.findings.resolved','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;

CREATE OR REPLACE FUNCTION fn_retention_purge_intake_commands(p_job_id UUID,p_batch_size INT,p_dry_run BOOLEAN) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM intake_control_commands c WHERE c.status IN('applied','rejected')
      AND COALESCE(c.applied_at,c.created_at)<now()-INTERVAL '365 days';
  ELSE
    WITH eligible AS (
      SELECT c.id FROM intake_control_commands c WHERE c.status IN('applied','rejected')
        AND COALESCE(c.applied_at,c.created_at)<now()-INTERVAL '365 days'
      ORDER BY COALESCE(c.applied_at,c.created_at),c.id LIMIT p_batch_size FOR UPDATE OF c SKIP LOCKED
    ), deleted AS (DELETE FROM intake_control_commands c USING eligible WHERE c.id=eligible.id RETURNING c.id)
    SELECT count(*) INTO v_affected FROM deleted;
  END IF;
  INSERT INTO assurance_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'assurance.intake_commands','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;

GRANT EXECUTE ON FUNCTION fn_assurance_summarize_failed_runs(INT) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_runs_failed(UUID,INT,BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_findings_resolved(UUID,INT,BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_intake_commands(UUID,INT,BOOLEAN) TO app_service;
