{{ config(materialized='view') }}

select
    report_date,
    currency,
    product,
    vendor,
    processed_volume_minor,
    recognized_fee_revenue_minor,
    modeled_variable_vendor_cost_minor,
    modeled_contribution_margin_minor,
    cost_model_version,
    cost_basis,
    metric_semantics,
    data_updated_at_utc
from {{ ref('fact_daily_unit_economics') }}
