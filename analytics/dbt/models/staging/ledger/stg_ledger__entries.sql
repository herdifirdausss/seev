{{ config(materialized='view') }}

select
    id,
    transaction_id,
    account_id,
    direction,
    amount_minor,
    balance_after_minor,
    created_at_utc,
    operation,
    is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
from {{ source('ledger_staging', 'ledger_entries_changes') }}
