# API contracts and schema evolution

The A9 baseline keeps consumed boundaries explicit:

- HTTP source contracts live in [`contracts/http/`](../../contracts/http/); generated
  bundles are produced with `make contract-generate` and are never hand-edited.
- Ownership, audience, consumers, and lifecycle are recorded in
  [`contracts/compatibility/surfaces.yaml`](../../contracts/compatibility/surfaces.yaml).
- AMQP routing keys and payload schemas are catalogued in
  [`contracts/events/catalog.yaml`](../../contracts/events/catalog.yaml).
- Protobuf remains canonical in `contracts/proto/`; behavioral metadata that Buf
  cannot infer is in [`proto-semantics.yaml`](../../contracts/compatibility/proto-semantics.yaml).

Before changing a consumed boundary, add an optional, additive version first.
Keep old and new representations live until consumers acknowledge the change,
the zero-use window has elapsed, and the replacement guide is published.
The only reviewed current exception is the Gateway → VendorService webhook
ownership cutover, which is recorded in
[`contracts/compatibility/approved-breaking.yaml`](../../contracts/compatibility/approved-breaking.yaml)
and linked to [archived Plan 54](../roadmap/archive/54-vendor-service-boundary.md);
it does not authorize unrelated breaking changes.

## Local gates

```bash
make contract-generate
make contract-lint
make contract-breaking
make contract-test
make contracts
```

`contract-breaking` compares existing operations and schemas with the checked-in
merge-base bundle. In CI, the merge-base is the actual pull-request base; local
bootstrap mode uses the repository's checked-in baseline. New operations, optional properties, and response statuses
are additive; removing or changing an existing operation, required field,
security requirement, request schema, response schema, enum, or unit fails.

Deprecated operations use `Deprecation`, `Sunset`, and a migration `Link`.
The checked-in policy requires a minimum 30-day window; traffic and consumer
acknowledgement must be zero before retirement.
