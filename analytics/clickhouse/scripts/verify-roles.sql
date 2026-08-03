-- Expected zero rows for every forbidden BI grant. Execute as a ClickHouse
-- access-management administrator in a disposable analytics environment.
SELECT grantee, privilege, database, table
FROM system.grants
WHERE grantee = 'metabase_bi'
  AND (database IN ('raw', 'staging') OR privilege IN ('INSERT', 'ALTER', 'CREATE TABLE', 'DROP TABLE', 'TRUNCATE'));

SELECT name, engine, create_table_query
FROM system.tables
WHERE database = 'raw'
  AND name = 'cdc_events'
  AND create_table_query NOT LIKE '%TTL ingested_at + INTERVAL 30 DAY DELETE%';
