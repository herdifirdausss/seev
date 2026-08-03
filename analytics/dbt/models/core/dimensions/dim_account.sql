{{ config(materialized='table') }}

select
    id as account_id,
    owner_type,
    account_type,
    currency,
    pocket_code,
    system_qualifier,
    status,
    created_at_utc,
    updated_at_utc
from {{ ref('stg_ledger__accounts_current') }}
