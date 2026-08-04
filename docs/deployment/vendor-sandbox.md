# Vendor sandbox contract

The repository's `mockvendor` and `mockvendor2` adapters are deterministic
development fixtures. They are useful for unit, integration, and CI tests but
are not evidence of a real provider integration and must never be enabled in
`staging` or `production`.

## Required staging configuration

The vendor workload receives:

- `VENDOR_SERVICE_ENABLED=true`;
- a certified provider route and provider-specific account identifier;
- `VENDOR_EGRESS_PROXY_REQUIRED=true`;
- `VENDOR_EGRESS_PROXY_URL` pointing at the private, allowlisted egress proxy;
- provider credentials from the cloud secret manager, mounted only into
  `vendor-service`;
- callback hostname, exact callback CIDRs, signature algorithm/key version,
  timeout, retry, and idempotency rules recorded in
  [`vendor-network-matrix.md`](vendor-network-matrix.md);
- a test ledger/account namespace that cannot settle real funds.

The real provider values are intentionally absent from Git. A release is not
vendor-certified until the operator attaches a redacted sandbox evidence
bundle containing the provider account, request/callback IDs, signatures,
duplicate callback result, timeout/retry result, and an operator approval.

## Safety rules

- `VENDOR_MOCKVENDOR_ENABLED` and `MOCKVENDOR2_ENABLED` must be `false` outside
  development/CI.
- No provider secret may be passed to gateway, auth, payin, payout, or ledger.
- Outbound traffic must traverse the allowlisted proxy; direct Internet
  egress is a failed deployment check.
- Callback requests require both source-network validation and a valid
  signature. A valid signature from an untrusted source is still rejected.
- Sandbox money is synthetic and must be bounded by a provider-side test
  account. Callback replay and conflicting-result tests are mandatory.

Run `scripts/ci/check-vendor-sandbox-config.sh` after rendering the staging
configuration. It validates presence and mode without echoing credentials.
