{{ config(materialized='incremental', unique_key='report_date||currency||transaction_type', on_schema_change='fail') }}

select
    report_date,
    currency,
    transaction_type,
    count() as quotes_created,
    countIf(is_consumed) as quotes_consumed,
    countIf(quote_state = 'expired') as quotes_expired,
    if(quotes_created = 0, toFloat64(0), quotes_consumed / quotes_created) as quote_conversion_rate,
    sum(quoted_fee_minor) as quoted_fee_minor,
    max(ingested_at) as data_updated_at_utc,
    'quote intent/conversion; not recognized revenue' as metric_semantics
from {{ ref('fact_fee_quote') }}
group by report_date, currency, transaction_type
{% if is_incremental() %}
having report_date >= (select max(report_date) - 2 from {{ this }})
{% endif %}
