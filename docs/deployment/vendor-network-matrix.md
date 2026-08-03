# Vendor network matrix

The current repository has an in-process mock adapter, so its callback and
outbound network values are learning defaults. A real vendor entry must add a
finite row here before K6/K9.

| Vendor | Callback hostname/path | Callback source CIDRs | Signature owner | Outbound hosts | Proxy required | Status |
|---|---|---|---|---|---|---|
| `mockvendor` | `callback.../webhooks/mockvendor` | local loopback/Calico test CIDRs only | VendorService | none; in-process mock | no network call today | sandbox |
| `mockvendor2` | `callback.../webhooks/mockvendor2` | local loopback/Calico test CIDRs only | VendorService | none; in-process mock | no network call today | optional sandbox |
| real vendor(s) | `UNKNOWN` | `UNKNOWN` | VendorService | `UNKNOWN` | yes | blocked until certified |

Rules:

- `VENDOR_CALLBACK_CIDRS` is application configuration and must contain only
  vendor-owned ranges in a cloud environment.
- The first chart uses a known Traefik L7 callback proxy, so cloud values must
  set `VENDOR_CALLBACK_TRUSTED_PROXY_CIDRS` to the exact cluster pod CIDR. The
  local profile leaves it empty because the local allowlist deliberately covers
  the Calico peer CIDR. A future direct L4/TLS-passthrough callback path must
  leave it empty and prove the original peer reaches VendorService directly.
- A valid callback signature is mandatory even when the source address is
  allowlisted.
- The shared NAT address proves stable egress, not unique VendorService
  identity.

The authoritative K0 record, including mock adapter behavior, proxy semantics,
and explicitly deferred real-provider fields, is
[deploy/inventory/vendor-network.yaml](../../deploy/inventory/vendor-network.yaml).
