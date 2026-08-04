# Runbook: Merchant API Key Compromise

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: operators.** Follow this procedure only in
> an environment where you are authorized to touch merchant credentials.

**Symptom:** a merchant reports a leaked API key, or you observe anomalous
traffic from one `merchant_api_keys` row (unexpected volume, unexpected
scopes exercised, requests from an unexpected environment).

## Immediate containment

1. Identify the key's public prefix from the merchant's report or from
   traffic logs (`X-Request-Id`-correlated access logs — the plaintext key
   is never logged, only `public_prefix`, per `services/gateway/internal/merchant/auth`'s
   own log-masking discipline).
2. Revoke it immediately via the Admin BFF merchant console
   (`/api/v1/admin/merchant`, "API keys" panel) or directly:
   ```http
   POST /api/v1/admin/gateway/tenants/{tenant_id}/keys/{key_id}/revoke
   ```
   Requires the maker role. Revocation takes effect immediately —
   `RequireMerchantAuth`'s `GetActiveByPrefix` only ever matches
   `status = 'active'`, so a revoked key fails the very next request with
   the same `401 invalid API key` response as an unknown key (never a
   distinguishable error — do not expect a different response code to
   confirm revocation; confirm via the audit log instead).
3. Confirm revocation landed: check the Admin BFF audit log
   (`/api/v1/admin/audit`) for a `POST .../revoke` entry with a `2xx`
   downstream status, or query directly:
   ```sql
   SELECT id, public_id, status, revoked_at FROM merchant_api_keys WHERE id = '<key_id>';
   ```

## Issue a replacement

4. Create a new key for the same tenant with the same scopes (or narrower,
   if the incident review suggests the original scope grant was too
   broad):
   ```http
   POST /api/v1/admin/gateway/tenants/{tenant_id}/keys
   ```
   The response's `plaintext` field is shown exactly once — hand it to the
   merchant through your own secure out-of-band channel (never paste it
   into a ticket, Slack, or email body that later gets archived in
   plaintext). It can never be re-fetched; a lost plaintext means creating
   yet another key, not "resending" this one.
5. If the compromise indicates the key's SECRET_DIGEST pepper itself may
   be compromised (a much larger incident — every key across every tenant
   uses the same `MERCHANT_API_KEY_PEPPER`), stop here and escalate: that
   requires rotating the pepper and re-issuing every key on the platform,
   not a single-key revocation.

## Follow-up

6. Review `merchant_api_key_scopes` for the compromised key against what
   the merchant actually needed — over-broad scopes are worth tightening
   on the replacement even if they weren't the actual attack vector.
7. If the traffic pattern suggests actual fraudulent transactions were
   posted before revocation, escalate to the ledger-integrity runbook
   ([ledger-integrity-alert.md](ledger-integrity-alert.md)) — key
   revocation stops NEW requests; it does not reverse anything already
   posted.
8. Consider whether the incident warrants suspending the tenant entirely
   while the merchant rotates every integration touching this key — see
   [merchant-tenant-suspension.md](merchant-tenant-suspension.md).
