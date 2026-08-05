{{ config(materialized='incremental', unique_key='id', on_schema_change='fail') }}

select
    id,
    transaction_id,
    account_id,
    direction,
    amount_minor,
    if(direction = 'credit', amount_minor, -amount_minor) as signed_amount_minor,
    balance_after_minor,
    created_at_utc,
    {{ report_date('created_at_utc') }} as report_date,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at,
    'ledger_entries' as source_table
from {{ ref('stg_ledger__entries') }}
where not is_deleted
{% if is_incremental() %}
and ingested_at >= (select date_sub(minute, 10, max(ingested_at)) from {{ this }})
{% endif %}
