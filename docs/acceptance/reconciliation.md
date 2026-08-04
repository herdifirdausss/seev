# Reconciliation acceptance

| Field | Value |
|---|---|
| Owner | Ledger/Ops |
| Code | `services/ledger/internal/ledger/recon`, settlement importers |

- [x] Imports are idempotent by batch/provider reference.
- [x] Comparison distinguishes missing, amount, currency, status, and duplicate mismatches.
- [x] Raw provider evidence is retained according to the retention policy.
- [x] A mismatch does not rewrite a posted transaction.
- [x] Resolution creates a maker-checker correction request and audit trail.
- [x] Reports expose counts, amounts, age, and unresolved items.
- [x] Operational mismatch runbook defines freeze, escalation, and replay.
- [ ] Production settlement sample and signed evidence bundle — environment evidence.

Verification:

```sh
go test ./services/ledger/internal/ledger/recon ./services/ledger/internal/...
make capability-e2e
```
