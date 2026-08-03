# K0 runtime journey record

All runtime checks used an isolated Compose project named `seev-k0`. The
existing `seev-local` kind cluster was not changed. Secret and certificate
values were supplied to processes or mounted by Compose and were not copied
into this record.

## R2 synthetic journey — measured

Passed against the app profile:

- registration and login with real JWTs;
- KYC level-1 approval and token refresh;
- top-up intent creation;
- signed `mockvendor` callback over the local TLS/mTLS boundary;
- top-up status reaching `settled`.

The bounded Docker sample is
[R2-20260802T235807Z.csv](../resources/R2-20260802T235807Z.csv). Network and
listener results are in [service probes](../service-probes/compose-app-20260802T235153Z.txt).

## R3 business journey — deferred, not accepted

The aligned local cryptographic key pair was supplied to the host harness and
the profile passed onboarding, KYC gating and upgrade, signed top-up,
transfer/withdraw fee accounting, quote immutability and single-use checks,
vendor failover setup, ledger integrity, operator reporting, dead-letter
inspection, and end-to-end request tracing. The final failover probe did not
observe its expected new `uncertain` vendor call because the reusable Redis
state retained a prior circuit condition. No clean R3 pass is claimed and no
host-process resource sample is used for sizing.

The harness URL defect found during this run was corrected in
`scripts/business-e2e.sh`: ledger internal fee-rule routes use the canonical
`/api/v1/ledger/admin/ledger/fee-rules` mount.

## R4 admin journey — deferred

The admin harness was started with the real local infrastructure, then stopped
during native linking after the existing kind cluster saturated local CPU.
No admin acceptance claim is made. This is a local capacity limitation, not a
Kubernetes behavior claim.

## R5/R6

The acknowledged disposable load profile and optional observability profile
remain deferred. They are outside the safe K0 host budget and do not alter the
first-deployment feature scope.
