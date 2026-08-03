{{ config(materialized='view') }}

select
    id,
    transaction_type,
    status,
    amount_minor,
    currency,
    nullIf(source_account_id, toUUID('00000000-0000-0000-0000-000000000000')) as source_account_id,
    nullIf(destination_account_id, toUUID('00000000-0000-0000-0000-000000000000')) as destination_account_id,
    external_ref,
    gateway,
    request_id,
    nullIf(closed_by_tx_id, toUUID('00000000-0000-0000-0000-000000000000')) as closed_by_tx_id,
    closed_reason,
    created_at_utc,
    updated_at_utc,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
from {{ source('ledger_staging', 'ledger_transactions_current') }}
