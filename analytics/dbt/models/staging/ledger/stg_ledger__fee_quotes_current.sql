{{ config(materialized='view') }}

select
    id,
    user_pseudonym,
    transaction_type,
    gateway,
    currency,
    amount_minor,
    quoted_fee_minor,
    fee_gateway,
    expires_at_utc,
    consumed_at_utc,
    consumed_by_ref,
    created_at_utc,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ source('ledger_staging', 'ledger_fee_quotes_current') }}
