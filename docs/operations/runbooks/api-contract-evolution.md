# API contract evolution runbook

> **Status: Current. Audience: API owners and operators.** Never remove a
> consumed operation because a local test has no callers.

## Trigger

Use this runbook when `contract-breaking`, an event/schema check, or a Buf gate
reports drift, or when a deprecated operation is approaching its sunset.

## Procedure

1. Preserve the failing artifact and identify the contract ID, owner, audience,
   and known consumers in `api/contracts/surfaces.yaml`.
2. Run `make contract-generate`, then `make contract-lint` to distinguish stale
   generated output from a semantic change.
3. If the change is the deliberate Gateway → VendorService callback cutover,
   verify it matches `api/contracts/approved-breaking.yaml` and the archived
   [Plan 54](../../roadmap/archive/54-vendor-service-boundary.md). Do not add
   unrelated operations or fields to that exception.
4. For any other breaking change, add a new major operation/schema, keep the old route
   or routing key live, and update the consumer acknowledgement in the catalog.
5. For retirement, verify the replacement guide, all consumer acknowledgements,
   the configured minimum window, and a zero-use period. Do not shorten the
   30-day policy in production configuration.
6. Run `make contract-breaking` and `make contract-test`; attach both outputs
   and the relevant contract diff to the change review.

## Safety checks

- Never put request bodies, event payloads, tokens, or PII in metric labels or
  validation logs.
- Unknown event fields are tolerated, but missing required fields, version or
  routing-key mismatches, and invalid units must have no partial business effect.
- If a gate cannot identify its merge base, stop and resolve repository state;
  do not compare against an arbitrary developer branch.
