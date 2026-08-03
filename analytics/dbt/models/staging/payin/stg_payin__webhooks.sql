{{ config(materialized='view') }}

select
    id,
    vendor,
    vendor_event_id,
    external_ref,
    user_pseudonym,
    amount_minor,
    currency,
    status,
    request_id,
    merchant_tenant_id,
    created_at_utc,
    updated_at_utc,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ source('payin_staging', 'payin_webhooks_changes') }}
where not is_deleted
