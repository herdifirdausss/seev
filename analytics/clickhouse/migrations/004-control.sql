CREATE TABLE IF NOT EXISTS control.pipeline_watermarks
(
    environment LowCardinality(String),
    source_service LowCardinality(String),
    source_table LowCardinality(String),
    max_source_lsn Nullable(UInt64),
    max_source_timestamp DateTime64(6, 'UTC'),
    max_ingested_at DateTime64(6, 'UTC'),
    latest_topic_offset Int64,
    observed_at DateTime64(6, 'UTC')
)
ENGINE = ReplacingMergeTree(observed_at)
ORDER BY (environment, source_service, source_table);

CREATE TABLE IF NOT EXISTS control.reconciliation_runs
(
    run_id UUID,
    environment LowCardinality(String),
    status LowCardinality(String),
    cutoff_type LowCardinality(String),
    cutoff_value String,
    started_at DateTime64(6, 'UTC'),
    finished_at Nullable(DateTime64(6, 'UTC')),
    critical_failures UInt32,
    warning_failures UInt32,
    details String
)
ENGINE = ReplacingMergeTree(finished_at)
ORDER BY (environment, run_id);

CREATE TABLE IF NOT EXISTS control.reconciliation_items
(
    run_id UUID,
    check_name LowCardinality(String),
    source_service LowCardinality(String),
    source_table_or_metric LowCardinality(String),
    warehouse_model LowCardinality(String),
    currency LowCardinality(String),
    report_date Nullable(Date),
    cutoff_type LowCardinality(String),
    cutoff_value String,
    expected_value Int64,
    actual_value Int64,
    delta_value Int64,
    severity LowCardinality(String),
    status LowCardinality(String),
    details String,
    created_at DateTime64(6, 'UTC')
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(created_at)
ORDER BY (created_at, run_id, check_name, source_service, currency)
TTL created_at + INTERVAL 180 DAY DELETE;

CREATE TABLE IF NOT EXISTS control.schema_fingerprints
(
    source_service LowCardinality(String),
    source_table LowCardinality(String),
    schema_fingerprint String,
    classification LowCardinality(String),
    observed_at DateTime64(6, 'UTC')
)
ENGINE = ReplacingMergeTree(observed_at)
ORDER BY (source_service, source_table);

CREATE TABLE IF NOT EXISTS control.dbt_invocations
(
    invocation_id UUID,
    environment LowCardinality(String),
    result LowCardinality(String),
    started_at DateTime64(6, 'UTC'),
    finished_at Nullable(DateTime64(6, 'UTC')),
    model_count UInt32,
    failure_count UInt32,
    artifact_path String
)
ENGINE = MergeTree
ORDER BY (started_at, invocation_id)
TTL started_at + INTERVAL 30 DAY DELETE;

CREATE TABLE IF NOT EXISTS control.data_quality_failures
(
    observed_at DateTime64(6, 'UTC'),
    check_name LowCardinality(String),
    layer LowCardinality(String),
    severity LowCardinality(String),
    failure_count UInt64,
    details String
)
ENGINE = MergeTree
ORDER BY (observed_at, check_name)
TTL observed_at + INTERVAL 180 DAY DELETE;

CREATE TABLE IF NOT EXISTS control.backfill_runs
(
    run_id UUID,
    source_service LowCardinality(String),
    source_table LowCardinality(String),
    status LowCardinality(String),
    started_at DateTime64(6, 'UTC'),
    finished_at Nullable(DateTime64(6, 'UTC')),
    source_cutoff String,
    details String
)
ENGINE = MergeTree
ORDER BY (started_at, run_id)
TTL started_at + INTERVAL 180 DAY DELETE;
