# VendorService boundary

VendorService is the only deployable component that owns vendor wire clients,
vendor signatures, callback ingress, and vendor callback evidence.

Outbound calls use mTLS gRPC:

`Payin/Payout -> VendorService -> vendor adapter`

Inbound callbacks use the restricted VendorService listener:

`vendor -> POST /webhooks/{vendor} -> inbox -> normalized Payin/Payout callback`

The callback edge rejects an unallowlisted source before reading the body,
supports forwarded addresses only from an explicit trusted-proxy CIDR list,
caps the body at 64 KiB, verifies the vendor signature over the raw bytes, and
stores the raw body and selected headers in `vendor.vendor_callback_inbox`.
Duplicate `(vendor, vendor_event_id)` deliveries replay the durable outcome.

Local development defaults to loopback callback sources. Set
`VENDOR_CALLBACK_CIDRS` and, only when applicable,
`VENDOR_CALLBACK_TRUSTED_PROXY_CIDRS` for a controlled edge. Do not broaden
these values to public networks without a vendor-specific signature and source
policy review.

Gateway no longer owns `/webhooks/{vendor}`. Payin and Payout receive only
normalized callbacks over their mTLS gRPC allowlist; neither callback contract
contains an authoritative Seev `user_id`.

## Manual chaos drill

Run only against the disposable local stack. The drill builds the current
binaries and uses synthetic top-up intents; it is not a production probe.

```bash
./scripts/vendor-boundary-chaos.sh restart
./scripts/vendor-boundary-chaos.sh duplicate
./scripts/vendor-boundary-chaos.sh lost-response
./scripts/vendor-boundary-chaos.sh all
```

The checks assert that callback redelivery after a VendorService restart,
duplicate delivery, and retry after a client-side lost response produce one
ledger effect. RabbitMQ/owner-service outage coverage remains in the broader
`scripts/chaos-test.sh` scenarios. Preserve `KEEP_WORK_DIR=1` when a failed
drill needs service logs for diagnosis.
