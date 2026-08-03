{{ config(materialized='view') }}

select
    environment,
    status,
    cutoff_type,
    cutoff_value,
    finished_at,
    critical_failures,
    warning_failures,
    details
from {{ source('control', 'reconciliation_runs') }}
order by finished_at desc
limit 1
