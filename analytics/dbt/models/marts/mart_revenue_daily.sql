{{ config(materialized='incremental', unique_key='report_date||currency', on_schema_change='fail') }}

select
    report_date,
    currency,
    multiIf(startsWith(transaction_type, 'money_in'), 'payin', startsWith(transaction_type, 'withdraw'), 'payout', transaction_type = 'transfer_p2p', 'transfer', 'other') as product,
    sum(recognized_fee_revenue_minor) as recognized_fee_revenue_minor,
    countIf(direction = 'credit') as fee_credit_entry_count,
    countIf(direction = 'debit') as fee_debit_entry_count,
    max(ingested_at) as data_updated_at_utc,
    'recognized revenue from posted Ledger fee-account entries; quote and volume excluded' as metric_semantics
from {{ ref('fact_fee_revenue') }}
group by report_date, currency, product
{% if is_incremental() %}
having report_date >= (select max(report_date) - 2 from {{ this }})
{% endif %}
