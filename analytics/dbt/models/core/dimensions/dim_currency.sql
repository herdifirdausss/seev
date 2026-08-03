{{ config(materialized='table') }}

select distinct currency
from {{ ref('fact_ledger_transaction') }}
where currency != ''
