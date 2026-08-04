# Merchant module

Gateway Merchant owns the tenant-facing B2B API and its operational state.

- `application/` — module composition, admin handlers, retention, webhook
  lifecycle, and observability orchestration.
- `api/` — public B2B HTTP surface.
- `auth/` — merchant API-key and global-flag behavior.
- `client/` — narrow clients for owner services.
- `idempotency/` — request idempotency policy.
- `lifecycle/` — merchant maker/checker lifecycle.
- `model/` — merchant data types.
- `quota/` — quota policy.
- `repository/` — Gateway-owned persistence.
- `webhook/` — endpoint management, event consumer, and relay.

`module.go` is the stable Merchant facade used by Gateway composition. It
contains no business logic.
