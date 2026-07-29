# Runbook: Merchant Idempotency Records Stuck

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: operators.**

**Alert:** `SeevMerchantIdempotencyStuckLeases`
(`deploy/observability/prometheus/rules/merchant.yml`) —
`seev_merchant_idempotency_stuck_leases > 0` for 15+ minutes.

**Symptom:** a merchant reports a request that appears to have never
completed (no response, or a client-side timeout), and retries with the
same `Idempotency-Key` keep returning `IDEMPOTENCY_IN_PROGRESS` instead of
either the original response or a fresh attempt.

## Understand the mechanism before you act

`internal/merchant/idempotency` (T4) claims a row in `'processing'` state
under a lease (`lease_owner`, `lease_expires_at`) before doing any
downstream work. Three things can happen next:

- **Normal path:** the request finishes, the row moves to `'completed'`
  or `'failed'`, the lease is cleared.
- **Self-healing path:** the original request handler crashed (pod
  restart, panic, network partition) before finishing — the row is stuck
  `'processing'` with a lease that will EXPIRE at `lease_expires_at`.
  `IdempotencyRepository.TakeoverExpiredLease` runs automatically the
  NEXT time a request arrives with the SAME `(tenant_id, operation_id,
  idempotency_key)` — it atomically reassigns the lease and lets the new
  request retry the operation. **Most "stuck" records resolve themselves
  the moment the merchant retries**, with no operator action needed.
- **The alert case:** nobody has retried yet, so the expired lease just
  sits there. This is not itself broken — it is exactly the state the
  system is designed to tolerate — but if it persists a long time it is
  worth understanding why the merchant hasn't retried (their own
  integration may be the one stuck, not Seev).

## Diagnose

1. Find the stuck records:
   ```sql
   SELECT id, tenant_id, operation_id, idempotency_key, lease_owner, lease_expires_at, created_at
   FROM merchant_idempotency_records
   WHERE state = 'processing' AND lease_expires_at < now()
   ORDER BY created_at;
   ```
2. Check `lease_owner` — if the same pod/instance name appears across many
   stuck rows, that instance likely crashed or was killed mid-request;
   check its own logs/exit code around `created_at`.
3. Check whether the DOWNSTREAM operation (`downstream_key`) actually
   completed despite the record itself being stuck — e.g. did the ledger
   transaction, payin intent, or payout actually post? If it did, the
   record being stuck is purely a bookkeeping gap, not a lost transaction
   — `TakeoverExpiredLease` will still let the retry proceed correctly
   because the operation's OWN idempotency (ledger's `SAVEPOINT
   sp_idem`-style dedup, or the vendor's own idempotency key) prevents a
   double-execution regardless of this record's state.

## Fix

4. **Do not manually flip `state` to `'completed'` or `'failed'`** — you
   do not know what response body the merchant is expecting, and a
   fabricated response could desync the merchant's own records from
   reality. The correct fix is always: let (or ask) the merchant retry
   with the same `Idempotency-Key`; `TakeoverExpiredLease` handles the
   rest.
5. If the merchant's own integration cannot retry (e.g. their own client
   gave up and discarded the key), and you've confirmed via step 3 that
   the downstream operation never actually executed, the merchant must
   submit a NEW request with a NEW idempotency key — there is no
   supported way to "resume" an abandoned key from the operator side.
6. If `SeevMerchantIdempotencyStuckLeases` fires broadly (many tenants,
   many operations, not one merchant's isolated retry gap), suspect an
   infrastructure-level problem: a rolling deploy that killed pods
   mid-request without graceful drain, or a database connectivity blip
   that caused every in-flight request to fail simultaneously. Check
   deploy timestamps and Postgres connection logs around when the alert
   started.

## Verify recovery

7. `SELECT count(*) FROM merchant_idempotency_records WHERE state =
   'processing' AND lease_expires_at < now();` trending back toward zero
   as retries land. The gauge (`seev_merchant_idempotency_records{state=
   "processing"}`, refreshed every 30s) and the stuck-lease count on the
   [Merchant B2B dashboard](../../../deploy/observability/grafana/dashboards/merchant-b2b.json)
   are the fastest confirmation.
