# ADR: VendorService owns vendor callback ingress

- Status: Accepted
- Date: 2026-08-04
- Scope: Payin/Payout vendor callback architecture

## Context

Vendor payment callbacks arrive from external parties over HTTP and must be
authenticated, bounded, and normalized before any business service acts on
them. The original design let Payin and Payout each handle their own raw
callbacks, creating duplicated HMAC verification, payload normalization, and
vendor-specific logic in two services with separate failure postures.

## Decision

VendorService (`services/vendor-service/`) is the sole callback ingress for
all external vendor traffic. It:

- authenticates source CIDR and HMAC signatures;
- bounds and normalizes callback payloads;
- persists `vendor_callback_inbox` before delivery;
- delivers a standardized, owner-neutral callback to Payin or Payout over mTLS gRPC.

Payin and Payout perform their own strict intent/vendor correlation after
receiving the normalized callback. They do not trust a `user_id` from the
vendor payload.

VendorService deliberately does NOT choose `user_id`, decide whether a
callback matches a business intent, post Ledger money, or own Payin/Payout
state. Vendor secrets are configured on VendorService only.

## Consequences

Vendor wire protocol changes affect only VendorService. Payin and Payout
each own their correlation logic independently. The callback inbox provides
duplicate-replay safety across a worker crash boundary. A vendor-specific
bug cannot directly cause a Ledger posting without going through the owning
service's correlation gate.
