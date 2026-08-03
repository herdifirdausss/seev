{{ config(materialized='incremental', unique_key='report_date||currency||product||vendor', on_schema_change='fail') }}

with revenue as (
    select
        report_date,
        currency,
        multiIf(startsWith(transaction_type, 'money_in'), 'payin', startsWith(transaction_type, 'withdraw'), 'payout', transaction_type = 'transfer_p2p', 'transfer', 'other') as product,
        sum(recognized_fee_revenue_minor) as recognized_fee_revenue_minor
    from {{ ref('fact_fee_revenue') }}
    group by report_date, currency, product
), volume as (
    select
        report_date,
        currency,
        multiIf(startsWith(transaction_type, 'money_in'), 'payin', startsWith(transaction_type, 'withdraw'), 'payout', transaction_type = 'transfer_p2p', 'transfer', 'other') as product,
        sumIf(amount_minor, is_posted) as successful_processed_volume_minor
    from {{ ref('fact_ledger_transaction') }}
    where product in ('payin', 'payout')
    group by report_date, currency, product
), schedule as (
    select * from {{ ref('vendor_cost_schedule') }}
)
select
    v.report_date,
    v.currency,
    v.product,
    coalesce(s.vendor, 'unmodeled') as vendor,
    v.successful_processed_volume_minor as processed_volume_minor,
    coalesce(r.recognized_fee_revenue_minor, 0) as recognized_fee_revenue_minor,
    toInt64(round(v.successful_processed_volume_minor * coalesce(s.variable_rate_basis_points, 0) / 10000)) as modeled_variable_vendor_cost_minor,
    coalesce(r.recognized_fee_revenue_minor, 0) - toInt64(round(v.successful_processed_volume_minor * coalesce(s.variable_rate_basis_points, 0) / 10000)) as modeled_contribution_margin_minor,
    coalesce(s.model_version, 'unmodeled') as cost_model_version,
    'modeled' as cost_basis,
    'recognized revenue minus modeled variable vendor cost; not net profit' as metric_semantics,
    now64(6, 'UTC') as data_updated_at_utc
from volume v
left join revenue r on r.report_date = v.report_date and r.currency = v.currency and r.product = v.product
left join schedule s
    on s.product = v.product
   and s.currency = v.currency
   and v.report_date >= toDate(s.effective_from)
   and v.report_date < toDate(s.effective_to)
{% if is_incremental() %}
where v.report_date >= (select max(report_date) - 2 from {{ this }})
{% endif %}
