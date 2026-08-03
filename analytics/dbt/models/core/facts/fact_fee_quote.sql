{{ config(materialized='incremental', unique_key='id', on_schema_change='fail') }}

select
    id,
    user_pseudonym,
    transaction_type,
    gateway,
    currency,
    amount_minor,
    quoted_fee_minor,
    fee_gateway,
    expires_at_utc,
    consumed_at_utc,
    consumed_by_ref,
    consumed_at_utc is not null as is_consumed,
    if(consumed_at_utc is not null, 'consumed', if(expires_at_utc < now64(6, 'UTC'), 'expired', 'open')) as quote_state,
    created_at_utc,
    {{ report_date('created_at_utc') }} as report_date,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ ref('stg_ledger__fee_quotes_current') }}
{% if is_incremental() %}
where ingested_at >= (select date_sub(minute, 10, max(ingested_at)) from {{ this }})
{% endif %}
