# Vendor service

Vendor service is the boundary around external payment providers. It owns
callback authentication, normalized callback delivery, outbound vendor
commands, and the vendor callback inbox. It does not decide Payin or Payout
business state.

## Ownership

- Owns vendor adapter registration, callback inbox/outbound-attempt records,
  callback source policy, and Vendor retention work.
- `contracts/vendorgw/` contains the transport-neutral adapter contract shared
  by Payin, Payout, and this service.
- Payin and Payout receive normalized results through generated RPC contracts;
  they do not import this service's `internal` package.

## Layout

- `cmd/vendor/` — process composition and callback/RPC listener wiring.
- `internal/` — callback, client, adapter, registry, server, and retention
  implementation. The package is private to this service. Synthetic provider
  behavior lives under `internal/adapter/mockvendor`; it is not part of the
  cross-service contract tree.
- `migrations/` — Vendor-owned schema changes.

The physical directory is `services/vendor-service` because `vendor` is a
reserved Go directory name. The architecture registry keeps the logical
canonical service name `vendor` and the binary remains `vendor`.

## Runtime and verification

Compile with `go test -run '^$' ./services/vendor-service/...`. Run the
callback and adapter integration tests with the service's Postgres and mTLS
dependencies available.
