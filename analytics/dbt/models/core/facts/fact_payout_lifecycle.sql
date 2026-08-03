{{ config(materialized='incremental', unique_key='payout_id', on_schema_change='fail') }}

with attempts as (
    select
        payout_request_id,
        count() as provider_attempt_count,
        countIf(outcome = 'accepted') as accepted_attempt_count,
        countIf(outcome = 'uncertain') as uncertain_attempt_count
    from {{ ref('stg_payout__calls') }}
    group by payout_request_id
)
select
    p.id as payout_id,
    p.user_pseudonym,
    p.amount_minor,
    p.currency,
    p.vendor,
    p.status,
    p.created_at_utc,
    p.updated_at_utc as terminal_at_utc,
    if(p.status = 'settled', p.amount_minor, toInt64(0)) as successful_amount_minor,
    p.status = 'settled' as is_successful,
    p.hold_tx_id,
    p.settle_tx_id as settlement_transaction_id,
    p.fee_quote_id,
    coalesce(a.provider_attempt_count, 0) as provider_attempt_count,
    coalesce(a.accepted_attempt_count, 0) as accepted_attempt_count,
    coalesce(a.uncertain_attempt_count, 0) as uncertain_attempt_count,
    dateDiff('millisecond', p.created_at_utc, p.updated_at_utc) as duration_to_terminal_ms,
    {{ report_date('p.created_at_utc') }} as report_date,
    p.source_lsn,
    p.partition,
    p.offset,
    p.ingested_at
from {{ ref('stg_payout__requests_current') }} p
left join attempts a on a.payout_request_id = p.id
{% if is_incremental() %}
where p.ingested_at >= (select date_sub(minute, 30, max(ingested_at)) from {{ this }})
{% endif %}
