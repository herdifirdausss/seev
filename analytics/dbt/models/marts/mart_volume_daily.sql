{{ config(materialized='incremental', unique_key='report_date||currency||product', on_schema_change='fail') }}

select
    report_date,
    currency,
    multiIf(startsWith(transaction_type, 'money_in'), 'payin', startsWith(transaction_type, 'withdraw'), 'payout', transaction_type = 'transfer_p2p', 'transfer', 'other') as product,
    sumIf(amount_minor, is_posted) as successful_processed_volume_minor,
    countIf(is_posted) as successful_transaction_count,
    countIf(status = 'failed') as failed_transaction_count,
    max(ingested_at) as data_updated_at_utc,
    'volume, not recognized revenue' as metric_semantics
from {{ ref('fact_ledger_transaction') }}
group by report_date, currency, product
{% if is_incremental() %}
having report_date >= (select max(report_date) - 2 from {{ this }})
{% endif %}
