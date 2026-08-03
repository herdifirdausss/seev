CREATE ROLE IF NOT EXISTS cdc_ingest;
CREATE ROLE IF NOT EXISTS dbt_transform;
CREATE ROLE IF NOT EXISTS bi_readonly;
CREATE ROLE IF NOT EXISTS ops_readonly;
CREATE ROLE IF NOT EXISTS reconciliation_read;
CREATE ROLE IF NOT EXISTS reconciliation_write_control;

GRANT INSERT ON raw.cdc_events TO cdc_ingest;
GRANT SELECT ON raw.cdc_events, raw.cdc_events_deduplicated TO ops_readonly;
GRANT SELECT ON raw.cdc_events_deduplicated TO dbt_transform;
GRANT SELECT, INSERT, ALTER, CREATE TABLE, CREATE VIEW, DROP TABLE ON staging.* TO dbt_transform;
GRANT SELECT, INSERT, ALTER, CREATE TABLE, CREATE VIEW, DROP TABLE ON core.* TO dbt_transform;
GRANT SELECT, INSERT, ALTER, CREATE TABLE, CREATE VIEW, DROP TABLE ON mart.* TO dbt_transform;
GRANT SELECT, INSERT, ALTER, CREATE TABLE, CREATE VIEW ON control.* TO dbt_transform;
GRANT SELECT ON mart.* TO bi_readonly;
GRANT SELECT ON core.dim_date, core.dim_currency, core.dim_transaction_type, core.dim_vendor TO bi_readonly;
GRANT SELECT ON control.pipeline_watermarks, control.reconciliation_runs, control.data_quality_failures TO bi_readonly;
GRANT SELECT ON core.*, mart.*, control.* TO reconciliation_read;
GRANT SELECT, INSERT, ALTER ON control.reconciliation_runs, control.reconciliation_items, control.data_quality_failures TO reconciliation_write_control;
