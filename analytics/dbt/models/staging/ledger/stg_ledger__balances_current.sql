{{ config(materialized='view') }}

select
    account_id,
    balance_minor,
    allow_negative,
    updated_at_utc,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ source('ledger_staging', 'ledger_balances_current') }}
