select
    transaction_id,
    sumIf(amount_minor, direction = 'debit') as debit_minor,
    sumIf(amount_minor, direction = 'credit') as credit_minor
from {{ ref('fact_ledger_entry') }}
group by transaction_id
having debit_minor != credit_minor
