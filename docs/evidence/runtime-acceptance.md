# Local runtime-acceptance evidence

This is the executable evidence contract for the `Runtime accepted` column in
[`capability-inventory.md`](../engineering/capability-inventory.md). It covers
the repository's disposable runtime profile; it does not claim a cloud apply,
real vendor certification, protected-registry admission, production traffic,
or owner sign-off. Those remain `Production ready` and external release gates.

## Run

From a checkout with Docker, Go, kind, kubectl, Helm, and jq installed:

```sh
RUNTIME_ACCEPTANCE_DIR=/tmp/seev-runtime-acceptance-evidence \
  ./scripts/runtime-acceptance.sh
```

The runner refuses to reuse an existing `seev-runtime-acceptance` Compose
project or kind cluster. It creates a dedicated Compose project on alternate
dependency host ports, so the ordinary `seev` development stack may remain
up, then runs the application and failure journeys from clean volumes,
renders the platform contracts, runs the supply-chain verifier, executes a
disposable kind deployment and pod-replacement drill, and runs latest/PITR DR
game days against explicitly named volumes and backup repositories. The final
manifest is sanitized and can be reviewed without rerunning the drills:

```sh
./scripts/ci/check-runtime-acceptance.sh \
  /tmp/seev-runtime-acceptance-evidence/runtime-manifest.txt
```

The runner removes the dedicated kind cluster on exit unless
`RUNTIME_KEEP_K8S_CLUSTER=1` is set. The DR drill always removes only its own
named game-day resources.

## Capability coverage

| Capability | Runtime stages | Result recorded as |
|---|---|---|
| Ledger posting and idempotency | business, merchant, core chaos | `accepted` |
| Unified command policy boundary | capability, capability chaos | `accepted` |
| KYC/tenant execution state | business, merchant, capability chaos | `accepted` |
| Scheduled and recurring transactions | capability, capability chaos | `accepted` |
| Fee pricing and approvals | capability, capability chaos | `accepted` |
| Pay-in and callback reconciliation | business, merchant, core chaos | `accepted` |
| Payout unknown-state recovery | capability, capability chaos | `accepted` |
| Disbursement maker-checker | capability, capability chaos | `accepted` |
| Chargeback dispute lifecycle | capability, capability chaos | `accepted` |
| Reconciliation and append-only corrections | capability, capability chaos | `accepted` |
| Multi-currency and FX | capability, capability chaos | `accepted` |
| Transactional outbox | business, merchant, core chaos | `accepted` |
| Auth and KYC | business, privacy, capability chaos | `accepted` |
| Container/Kubernetes deployment | container, Kubernetes E2E, Kubernetes chaos | `accepted` |
| IaC and workload identity | platform integration, platform E2E, platform chaos | `accepted` |
| Supply-chain controls | supply-chain E2E | `accepted` |
| DR and restore | latest and PITR game days | `accepted` |

`accepted` here means that the local runtime profile passed for the reviewed
commit and produced a valid manifest with all 17 capability rows. It is not a
production-ready decision; the production column remains blocked until the
environment-specific evidence and approvals described by the production
scorecard exist.
