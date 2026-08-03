{{ config(materialized='view') }}

select
    id,
    payout_request_id,
    vendor,
    attempt,
    response_status,
    outcome,
    created_at_utc,
    source_lsn,
    partition,
    offset,
    ingested_at
from {{ source('payout_staging', 'payout_calls_changes') }}
where not is_deleted
