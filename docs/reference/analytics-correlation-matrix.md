# C2 correlation inventory

See the canonical [correlation contract](../../analytics/contracts/correlation-matrix.md).

The implementation uses exact UUID/reference relationships for fee quotes,
Ledger entries, payout holds/settlements, and Ledger reversals. Payin rows are
not force-joined to Ledger because the current owner schema lacks a persisted
Ledger transaction key. Unlinked legacy records are a classified control
result, not a hidden exclusion.
