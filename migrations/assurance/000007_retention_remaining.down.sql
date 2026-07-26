DROP FUNCTION IF EXISTS fn_retention_purge_intake_commands(UUID,INT,BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_findings_resolved(UUID,INT,BOOLEAN);
DROP FUNCTION IF EXISTS fn_retention_purge_runs_failed(UUID,INT,BOOLEAN);
DROP FUNCTION IF EXISTS fn_assurance_summarize_failed_runs(INT);
DROP TABLE IF EXISTS assurance_incident_summaries;
