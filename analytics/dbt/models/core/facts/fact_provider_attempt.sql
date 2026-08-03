{{ config(materialized='incremental', unique_key='attempt_id', on_schema_change='fail') }}

select
    id as attempt_id,
    payout_request_id as owner_resource_id,
    'payout' as owner_type,
    vendor,
    attempt as attempt_number,
    created_at_utc as started_at_utc,
    response_status,
    outcome,
    multiIf(outcome = 'accepted', 'accepted', outcome = 'rejected', 'business_rejection', 'uncertain') as result_category,
    {{ report_date('created_at_utc') }} as report_date,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ ref('stg_payout__calls') }}
{% if is_incremental() %}
where ingested_at >= (select date_sub(minute, 30, max(ingested_at)) from {{ this }})
{% endif %}
