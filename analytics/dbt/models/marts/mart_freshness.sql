{{ config(materialized='table') }}

select
    'ledger' as source_service,
    'ledger_entries' as source_table,
    max(source_timestamp) as latest_source_timestamp_utc,
    max(ingested_at) as latest_ingested_at_utc,
    dateDiff('second', max(ingested_at), now64(6, 'UTC')) as freshness_seconds
from {{ ref('stg_ledger__entries') }}
