{{ config(materialized='incremental', unique_key='entry_id', on_schema_change='fail') }}

select
    e.id as entry_id,
    e.transaction_id,
    e.account_id as fee_account_id,
    t.transaction_type,
    a.system_qualifier as fee_account_qualifier,
    e.direction,
    e.amount_minor as entry_amount_minor,
    if(e.direction = 'credit', e.amount_minor, -e.amount_minor) as recognized_fee_revenue_minor,
    t.currency as currency,
    e.created_at_utc as posted_at_utc,
    {{ report_date('e.created_at_utc') }} as report_date,
    t.status = 'posted' as is_posted,
    e.source_lsn as source_lsn,
    e.partition as partition,
    e.offset as offset,
    e.ingested_at as ingested_at
from {{ ref('fact_ledger_entry') }} e
inner join {{ ref('stg_ledger__accounts_current') }} a on a.id = e.account_id and a.account_type = 'fee'
inner join {{ ref('fact_ledger_transaction') }} t on t.id = e.transaction_id
where t.status = 'posted'
{% if is_incremental() %}
  and e.ingested_at >= (select date_sub(minute, 10, max(ingested_at)) from {{ this }})
{% endif %}
