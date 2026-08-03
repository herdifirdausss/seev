select id, operation
from {{ ref('stg_ledger__entries') }}
where operation in ('u', 'd')
