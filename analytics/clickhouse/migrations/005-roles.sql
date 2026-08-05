CREATE ROLE IF NOT EXISTS cdc_ingest;
CREATE ROLE IF NOT EXISTS dbt_transform;
CREATE ROLE IF NOT EXISTS bi_readonly;
CREATE ROLE IF NOT EXISTS ops_readonly;
CREATE ROLE IF NOT EXISTS reconciliation_read;
CREATE ROLE IF NOT EXISTS reconciliation_write_control;

-- ClickHouse GRANT accepts exactly one db.table target per statement (the
-- comma list after ON only works for privileges, not targets), so each
-- multi-table grant below is repeated once per table instead.
GRANT INSERT ON raw.cdc_events TO cdc_ingest;
GRANT SELECT ON raw.cdc_events TO ops_readonly;
GRANT SELECT ON raw.cdc_events_deduplicated TO ops_readonly;
GRANT SELECT ON raw.cdc_events_deduplicated TO dbt_transform;
-- Several core/* fact models select from raw.cdc_events directly (not only
-- through the deduplicated view), so dbt_transform needs SELECT on both.
GRANT SELECT ON raw.cdc_events TO dbt_transform;
GRANT SELECT, INSERT, ALTER, CREATE TABLE, CREATE VIEW, DROP TABLE, DROP VIEW ON staging.* TO dbt_transform;
GRANT SELECT, INSERT, ALTER, CREATE TABLE, CREATE VIEW, DROP TABLE, DROP VIEW ON core.* TO dbt_transform;
GRANT SELECT, INSERT, ALTER, CREATE TABLE, CREATE VIEW, DROP TABLE, DROP VIEW ON mart.* TO dbt_transform;
-- DROP TABLE covers the dbt-clickhouse adapter's own _tmp_replace_* staging
-- tables used for atomic view/table replacement; TRUNCATE covers seed loads.
GRANT SELECT, INSERT, ALTER, CREATE TABLE, CREATE VIEW, DROP TABLE, DROP VIEW, TRUNCATE ON control.* TO dbt_transform;
GRANT SELECT ON mart.* TO bi_readonly;
GRANT SELECT ON core.dim_date TO bi_readonly;
GRANT SELECT ON core.dim_currency TO bi_readonly;
GRANT SELECT ON core.dim_transaction_type TO bi_readonly;
GRANT SELECT ON core.dim_vendor TO bi_readonly;
GRANT SELECT ON control.pipeline_watermarks TO bi_readonly;
GRANT SELECT ON control.reconciliation_runs TO bi_readonly;
GRANT SELECT ON control.data_quality_failures TO bi_readonly;
-- mart_unit_economics_daily is a VIEW (not a materialized table); ClickHouse
-- re-executes a view's query with the querying user's own privileges, so
-- bi_readonly needs direct SELECT on the core fact it wraps (confirmed
-- 2026-08-05: ACCESS_DENIED without this, even though the view itself is
-- granted via mart.*).
GRANT SELECT ON core.fact_daily_unit_economics TO bi_readonly;
-- The committed pay-in/payout performance dashboard specs
-- (analytics/metabase/dashboards/pay{in,out}-performance.yaml) query these
-- core lifecycle facts directly — no dedicated daily mart exists yet for
-- them — which plan section 22.1 explicitly allows ("approved core views").
GRANT SELECT ON core.fact_payin_lifecycle TO bi_readonly;
GRANT SELECT ON core.fact_payout_lifecycle TO bi_readonly;
GRANT SELECT ON core.fact_provider_attempt TO bi_readonly;
GRANT SELECT ON core.* TO reconciliation_read;
GRANT SELECT ON mart.* TO reconciliation_read;
GRANT SELECT ON control.* TO reconciliation_read;
GRANT SELECT, INSERT, ALTER ON control.reconciliation_runs TO reconciliation_write_control;
GRANT SELECT, INSERT, ALTER ON control.reconciliation_items TO reconciliation_write_control;
GRANT SELECT, INSERT, ALTER ON control.data_quality_failures TO reconciliation_write_control;
