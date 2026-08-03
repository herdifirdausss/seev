{{ config(materialized='view') }}

select
    id,
    user_pseudonym,
    amount_minor,
    currency,
    vendor,
    status,
    hold_tx_id,
    settle_tx_id,
    vendor_ref,
    fee_quote_id,
    fee_amount_minor,
    fee_gateway,
    request_id,
    merchant_tenant_id,
    downstream_key,
    created_at_utc,
    updated_at_utc,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ source('payout_staging', 'payout_requests_current') }}
