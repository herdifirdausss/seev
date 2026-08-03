{{ config(materialized='view') }}

select
    id,
    reference,
    user_pseudonym,
    amount_minor,
    currency,
    vendor,
    status,
    settled_event_id,
    expires_at_utc,
    request_id,
    merchant_tenant_id,
    downstream_key,
    created_at_utc,
    updated_at_utc,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ source('payin_staging', 'payin_intents_current') }}
