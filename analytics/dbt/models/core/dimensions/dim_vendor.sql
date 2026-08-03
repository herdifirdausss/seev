{{ config(materialized='table') }}

select vendor
from {{ ref('fact_payout_lifecycle') }}
where vendor != ''
group by vendor
