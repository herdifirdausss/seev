{{ config(materialized='table') }}

select gateway
from {{ ref('fact_ledger_transaction') }}
where gateway is not null and gateway != ''
group by gateway
