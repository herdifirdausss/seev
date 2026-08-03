select entry_id
from {{ ref('fact_fee_revenue') }}
where not is_posted
