# Scheduled transactions acceptance

| Field | Value |
|---|---|
| Owner | Ledger |
| Code | `services/ledger/internal/ledger/schedule`, `services/ledger/internal/ledger/command` |
| Schema | scheduled transaction/occurrence migrations |

- [x] Public users can create only the allowed transfer types.
- [x] Ownership checks protect schedule and occurrence reads/writes.
- [x] Occurrences have immutable idempotency keys and attempt history.
- [x] Fee policy and consent cap are evaluated at execution time.
- [x] Execution re-checks subject, KYC, tenant, policy, and idempotency state.
- [x] Lease/crash/retry paths keep the same occurrence key.
- [x] Invalid or expired KYC is a business failure, not a bypass.
- [x] Admin retry is explicit and audited.
- [ ] Live multi-replica run and retained metrics artifact — environment evidence.

Verification:

```sh
go test ./services/ledger/internal/ledger/schedule ./services/ledger/internal/ledger/command
make capability-e2e
```
