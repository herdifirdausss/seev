CREATE MATERIALIZED VIEW IF NOT EXISTS control.mv_pipeline_watermarks
TO control.pipeline_watermarks
AS
SELECT
    'local-dev' AS environment,
    source_service,
    source_table,
    source_lsn AS max_source_lsn,
    source_timestamp AS max_source_timestamp,
    ingested_at AS max_ingested_at,
    offset AS latest_topic_offset,
    ingested_at AS observed_at
FROM raw.cdc_events;
