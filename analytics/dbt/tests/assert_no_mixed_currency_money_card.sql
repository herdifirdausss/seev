select report_date, product
from {{ ref('mart_volume_daily') }}
where currency = ''
