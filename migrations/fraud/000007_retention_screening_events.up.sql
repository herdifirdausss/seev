-- Durable aggregate proof that survives deletion of individual screening
-- events. The primary key makes repeated retention runs idempotent.
CREATE TABLE fraud_screening_event_summaries (
    bucket_date DATE NOT NULL,
    rule TEXT NOT NULL,
    verdict TEXT NOT NULL,
    event_count BIGINT NOT NULL CHECK (event_count >= 0),
    amount_minor_sum NUMERIC(30,0) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_date, rule, verdict)
);
GRANT SELECT, INSERT, UPDATE ON fraud_screening_event_summaries TO app_service;
GRANT SELECT ON fraud_screening_event_summaries TO app_readonly;
ALTER TABLE fraud_screening_event_summaries ENABLE ROW LEVEL SECURITY;
ALTER TABLE fraud_screening_event_summaries FORCE ROW LEVEL SECURITY;
CREATE POLICY fraud_screening_summaries_service ON fraud_screening_event_summaries
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY fraud_screening_summaries_readonly ON fraud_screening_event_summaries
    FOR SELECT TO app_readonly USING (true);

CREATE OR REPLACE FUNCTION fn_retention_purge_screening_events(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_affected INT; v_started TIMESTAMPTZ:=clock_timestamp();
BEGIN
  IF p_batch_size<1 OR p_batch_size>500 THEN RAISE EXCEPTION 'invalid retention batch size'; END IF;
  IF p_dry_run THEN
    SELECT count(*) INTO v_affected FROM screening_events e
    WHERE e.created_at<now()-INTERVAL '365 days'
      AND NOT fn_fraud_retention_hold_covers('screening_events',e.user_id::text,e.id::text,e.created_at);
  ELSE
    WITH eligible AS MATERIALIZED (
      SELECT e.id,e.created_at::date bucket_date,e.rule,e.verdict,e.amount
      FROM screening_events e
      WHERE e.created_at<now()-INTERVAL '365 days'
        AND NOT fn_fraud_retention_hold_covers('screening_events',e.user_id::text,e.id::text,e.created_at)
      ORDER BY e.created_at,e.id LIMIT p_batch_size FOR UPDATE OF e SKIP LOCKED
    ), summarized AS (
      INSERT INTO fraud_screening_event_summaries(bucket_date,rule,verdict,event_count,amount_minor_sum)
      SELECT bucket_date,rule,verdict,count(*),sum(amount) FROM eligible
      GROUP BY bucket_date,rule,verdict
      ON CONFLICT(bucket_date,rule,verdict) DO UPDATE SET
        event_count=fraud_screening_event_summaries.event_count+EXCLUDED.event_count,
        amount_minor_sum=fraud_screening_event_summaries.amount_minor_sum+EXCLUDED.amount_minor_sum,
        updated_at=now()
      RETURNING bucket_date
    ), deleted AS (
      DELETE FROM screening_events e USING eligible x WHERE e.id=x.id RETURNING e.id
    )
    SELECT count(*) INTO v_affected FROM deleted;
  END IF;
  INSERT INTO fraud_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
  VALUES(p_job_id,'fraud.screening_events','delete',p_dry_run,v_affected,1,v_started,'ok');
  RETURN v_affected;
END $$;
GRANT EXECUTE ON FUNCTION fn_retention_purge_screening_events(UUID,INT,BOOLEAN) TO app_service;
