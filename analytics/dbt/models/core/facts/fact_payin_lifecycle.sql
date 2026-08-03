{{ config(materialized='incremental', unique_key='payin_id', on_schema_change='fail') }}

with callbacks as (
    select
        external_ref,
        count() as callback_count,
        countDistinct(vendor_event_id) as distinct_callback_count,
        maxIf(created_at_utc, status in ('posted', 'failed', 'blocked', 'ignored')) as terminal_callback_at_utc
    from {{ ref('stg_payin__webhooks') }}
    group by external_ref
)
select
    p.id as payin_id,
    p.user_pseudonym,
    p.amount_minor,
    p.currency,
    p.vendor,
    p.status,
    p.created_at_utc,
    p.updated_at_utc as terminal_at_utc,
    if(p.status in ('settled', 'posted'), p.amount_minor, toInt64(0)) as successful_amount_minor,
    p.status in ('settled', 'posted') as is_successful,
    p.settled_event_id,
    cast(null, 'Nullable(UUID)') as ledger_transaction_id,
    coalesce(c.callback_count, 0) as callback_count,
    coalesce(c.distinct_callback_count, 0) as distinct_callback_count,
    dateDiff('millisecond', p.created_at_utc, p.updated_at_utc) as duration_to_terminal_ms,
    {{ report_date('p.created_at_utc') }} as report_date,
    p.source_lsn,
    p.partition,
    p.offset,
    p.ingested_at
from {{ ref('stg_payin__intents_current') }} p
left join callbacks c on c.external_ref = p.reference
{% if is_incremental() %}
where p.ingested_at >= (select date_sub(minute, 30, max(ingested_at)) from {{ this }})
{% endif %}
