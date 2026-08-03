{{ config(materialized='table') }}

select distinct
    transaction_type,
    multiIf(startsWith(transaction_type, 'money_in'), 'payin', startsWith(transaction_type, 'withdraw'), 'payout', transaction_type = 'transfer_p2p', 'transfer', 'other') as product
from {{ ref('fact_ledger_transaction') }}
