{{ config(materialized='view') }}

select
    id,
    owner_type,
    account_type,
    currency,
    pocket_code,
    system_qualifier,
    status,
    created_at_utc,
    updated_at_utc,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ source('ledger_staging', 'ledger_accounts_current') }}
