{{ config(materialized='incremental', unique_key='id', on_schema_change='fail') }}

select
    id,
    transaction_type,
    status,
    amount_minor,
    currency,
    source_account_id,
    destination_account_id,
    external_ref,
    gateway,
    request_id,
    closed_by_tx_id,
    closed_reason,
    created_at_utc,
    updated_at_utc,
    {{ report_date('created_at_utc') }} as report_date,
    status = 'posted' as is_posted,
    status in ('posted', 'reversed') as is_terminal,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
from {{ ref('stg_ledger__transactions_current') }}
{% if is_incremental() %}
where ingested_at >= (select date_sub(minute, 10, max(ingested_at)) from {{ this }})
{% endif %}
