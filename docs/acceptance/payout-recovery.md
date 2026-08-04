# Payout recovery acceptance

| Field | Value |
|---|---|
| Owner | Payout/Ledger |
| Code | `services/payout`, `services/ledger` |

- [x] Vendor identity and request id are persisted before submission.
- [x] Timeout/ambiguous response becomes `unknown`, never an automatic new payout.
- [x] Recovery queries the original vendor and reconciles the same intent.
- [x] Ledger closed-state/idempotency guards prevent double settlement.
- [x] Duplicate and delayed callbacks converge on one outcome.
- [x] Operator actions and replay are exposed through the recovery runbook.
- [x] Chaos scenario 8 verifies unknown-state recovery locally.
- [ ] Configured real vendor sandbox run — external credential/evidence gate.

Verification:

```sh
go test ./services/payout/internal/... ./services/ledger/internal/...
KEEP_WORK_DIR=1 ./scripts/chaos-test.sh 8
```
