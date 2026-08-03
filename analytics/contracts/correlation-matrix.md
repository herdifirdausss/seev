# C2 deterministic correlation matrix

Analytics joins only on persisted identifiers. Amount, timestamp, user, vendor,
or status similarity is not a join key.

| Journey | Source record | Target record | Deterministic key | Status |
| --- | --- | --- | --- | --- |
| Fee quote conversion | `ledger.fee_quotes` | consuming operation | `consumed_by_ref` (`tx:<uuid>` or `payout:<uuid>`) | implemented |
| Fee recognition | `ledger.ledger_transactions` / entries | fee account entry | `ledger_entries.transaction_id` + account `type=fee` | implemented |
| Payout hold | `payout.payout_requests` | Ledger transaction | `hold_tx_id` | implemented |
| Payout settlement/release | `payout.payout_requests` | Ledger transaction | `settle_tx_id` | implemented |
| Pay-in callback | `payin.payin_webhook_events` | top-up intent | `external_ref = reference` | owner-service link only |
| Pay-in Ledger credit | Pay-in owner row | Ledger transaction | no persisted `ledger_transaction_id` in current schema | explicit legacy gap |
| Ledger reversal/refund | Ledger transaction | closing Ledger transaction | `closed_by_tx_id` | implemented |
| Merchant ownership | Payin/Payout owner row | Gateway merchant | `merchant_tenant_id` | deferred C1 enrichment |

The Pay-in Ledger link is not inferred from amount or timestamp. New Pay-in
rows without a deterministic Ledger transaction identifier remain unlinked and
are reported as classified legacy/data-contract gaps. A future additive
owner-service migration may populate such a field; the analytics platform must
not write it.
