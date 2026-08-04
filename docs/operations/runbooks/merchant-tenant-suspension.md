# Runbook: Merchant Tenant Suspension

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: operators.** Follow this procedure only in
> an environment where you are authorized to act on merchant accounts.

**When to use this:** a single merchant tenant needs to stop transacting
immediately — suspected fraud, a contractual dispute, a compliance hold,
or the merchant's own request — without affecting any other tenant and
without touching the merchant's existing data.

This is narrower than the global kill switch
(`services/gateway/internal/merchant/auth.GlobalFlag`, T9's own "global route-disable
control") — suspension acts on ONE tenant; the kill switch acts on the
entire merchant B2B surface for every tenant at once. Use suspension for
a single-merchant incident; reserve the kill switch for a platform-wide
one (see that control's own admin route,
`PUT /api/v1/admin/gateway/global/b2b-api`, checker-gated).

## Suspend

1. Suspend via the Admin BFF console or directly:
   ```http
   POST /api/v1/admin/gateway/tenants/{tenant_id}/suspend
   ```
   Maker role only — no checker approval is required for suspension
   (§16.3's own maker-checker table lists only live-mode activation and
   closure as checker-gated; suspension is a reversible, immediately
   protective action).
2. **This takes effect immediately and structurally, not eventually**:
   `RequireMerchantAuth` (`services/gateway/internal/merchant/auth/middleware.go`) checks
   `tenant.Status` on every authenticated request, before any other
   processing. A suspended tenant's very next request — with the SAME
   still-valid API key — receives `403 tenant suspended`, distinct from
   every other failure mode (`401` for anything else). There is no
   propagation delay, no cache to invalidate, no separate flag to flip.
3. Confirm:
   ```sql
   SELECT status, suspended_by, suspended_at FROM merchant_tenants WHERE id = '<tenant_id>';
   ```

## What suspension does NOT do

4. **Existing accepted writes remain fully queryable.** Suspension only
   gates the authentication middleware in front of NEW inbound requests —
   it does not touch `merchant_idempotency_records`, ledger transactions,
   payin/payout rows, or webhook deliveries already created for this
   tenant. A suspended merchant's past transaction history, if exposed
   through an operator-only read path, is unaffected.
5. It does not cancel in-flight webhook deliveries — T7's relay worker has
   no tenant-status check of its own; a delivery already queued for this
   tenant will still be attempted on schedule. If you need deliveries to
   also stop, disable the specific webhook endpoint(s)
   (`POST .../webhooks/{id}/disable`) separately.
6. It does not revoke the tenant's API keys — they remain `active` in
   `merchant_api_keys`, they simply can no longer authenticate successfully
   while the tenant itself is suspended. Revoke keys separately if the
   incident specifically calls for key invalidation (see
   [merchant-api-key-compromise.md](merchant-api-key-compromise.md)).

## Reactivate

7. There is no dedicated "unsuspend" endpoint distinct from the general
   status-transition path — reactivating a suspended tenant back to
   `active` uses the same maker-checker lifecycle flow as a live
   activation would (`POST .../lifecycle/propose` with
   `action: "activate"`, then a DIFFERENT identity approving via
   `POST /api/v1/admin/gateway/lifecycle/{id}/approve`). This is
   deliberate: un-suspending is exactly as sensitive as the original
   activation and gets the same two-person control.
8. Confirm reactivation the same way as step 3, and confirm the merchant
   can authenticate again with a fresh request before telling them the
   incident is resolved.
