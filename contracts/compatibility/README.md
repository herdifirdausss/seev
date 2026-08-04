# Contract inventory

`surfaces.yaml` is the A9 contract inventory. It records live HTTP
registrations, gRPC services/methods, and AMQP event ownership alongside the
canonical OpenAPI and JSON Schema artifacts introduced by T1 and T4.

The inventory deliberately distinguishes:

- `operation`: a leaf route with one observable method/path contract;
- `mount`: a prefix registered by a parent router, whose leaf operations live
  in the mounted module;
- `operational` and `browser`: routes that remain in the inventory but are not
  business JSON operations;
- `owner` and `behavior_owner`: the edge or registration owner versus the
  service that defines the business behavior behind a proxy.

All identifiers, consumers, and examples are repository roles. They must not
contain credentials, email addresses, real UUIDs, or personal data.

Known current wire inconsistencies are recorded separately in
`known-inconsistencies.yaml`; they are inputs to T1/T2 and are not frozen as
the intended v1 contract. The only intentionally approved breaking cutover is
listed in `approved-breaking.yaml` and is tied to Plan 54's Gateway →
VendorService callback ownership migration; other breaking changes remain
blocked by `contract-breaking`.

`go test ./contracts/compatibility` also runs event-schema mutation fixtures. Optional
properties are accepted as additive v1 changes; removal, type changes, and new
required properties are rejected as v2-level changes.
