# 52 — Track A9: API Contracts and Schema Evolution

> [Documentation home](../../README.md) · [Roadmap](../README.md) · [Active plans](README.md)

> Derived from track **A9** in
> [42-long-term-roadmap.md](../42-long-term-roadmap.md).
>
> **Status: ready for execution; not implemented.** The activation trigger is
> a conscious learning decision made on 2026-07-22. Completing A9 is mandatory
> before the merchant/B2B API in C1 may begin.

## 1. Trigger and objective

The repository has stable service boundaries, but its compatibility protection
is uneven. Protobuf contracts have Buf lint and breaking checks, while HTTP
contracts are implicit in Go handlers and event compatibility is represented
only by Go structs, prose, and golden JSON tests. A route, status code, error
shape, JSON field, or event meaning can therefore change without a repository-
wide compatibility gate.

This is the correct time to establish a baseline: the current user APIs already
support complete product journeys, and C1 will introduce consumers that cannot
be upgraded in lockstep with this repository. A9 makes every consumed boundary
explicit, testable, versioned, and safe to deprecate before that external
surface is added.

### Measurable targets

1. Every registered HTTP route is classified by owner and audience; every JSON,
   CSV, multipart, or binary API route maps to exactly one OpenAPI operation.
2. All OpenAPI documents lint, bundle deterministically, and pass a semantic
   compatibility comparison against the merge-base contract.
3. Every API operation has at least one valid request/response fixture, and
   every documented error response uses the repository's standard envelope.
4. Every protobuf file passes generation, lint, and breaking checks; a synthetic
   forbidden change proves that the breaking gate fails.
5. Every published AMQP routing key has a catalog entry, JSON Schema, producer,
   consumer list, delivery guarantee, ownership, and compatibility state.
6. Existing event consumers accept unknown optional fields and reordered JSON,
   while rejecting malformed known-version payloads without applying effects.
7. Synthetic additive changes pass and synthetic breaking HTTP, protobuf, and
   event changes fail in CI.
8. A deprecated HTTP operation or contract version exposes standards-compliant
   metadata and cannot be retired without its minimum window, consumer
   acknowledgement, and measured zero-use gate.
9. Contract checks complete inside the PR gate without requiring a running
   external schema registry.

## 2. Live repository facts

These facts were verified when this plan was written. Execution must recheck the
live tree before changing contracts.

### 2.1 HTTP surfaces

- Eight deployable Go services register routes with Go 1.22 `http.ServeMux`.
- Auth exposes user routes under `/api/v1` and separate admin KYC routes on its
  internal listener.
- Gateway exposes user top-up, payout, notification, and proxied ledger routes
  under `/api/v1`; vendor callbacks use `POST /webhooks/{vendor}`.
- Ledger, pay-in, payout, fraud, assurance, and auth expose direct internal or
  admin HTTP routes. Admin BFF proxies selected operations and also serves HTML.
- Route registration is distributed across `cmd/` and `internal/` packages and
  can be conditional on configured dependencies. `ServeMux` does not expose a
  complete route inventory for compatibility tests.
- No OpenAPI document, HTTP semantic-diff gate, or route-to-contract coverage
  check exists.

### 2.2 HTTP representation behavior

- `pkg/response.Envelope` defines a useful success/error/meta JSON shape and
  `pkg/response.Decode` rejects unknown request fields.
- Not all API paths use that shape. Some middleware returns ad hoc JSON, several
  assurance/admin paths call `http.Error`, and default 404/405 responses are
  plain text.
- CSV statements/reports, KYC document download, multipart upload, health
  probes, metrics, and Admin BFF HTML intentionally do not use a JSON envelope.
- Several handlers serialize domain structs or `map[string]any` directly, which
  allows implementation fields to drift into a public response accidentally.
- Internal clients currently use Go's default tolerant JSON decoding, but that
  behavior is not asserted as a compatibility requirement.

### 2.3 gRPC contracts

- Five protobuf files define ledger, pay-in, payout, fraud, and ping services.
- `buf.yaml` uses STANDARD lint and FILE-level breaking rules.
- `make proto`, `make proto-lint`, and `make proto-breaking` exist, and generated
  Go bindings are committed.
- Plan 36 already requires additive protobuf changes, but package-version,
  deprecation, reserved-field, rollout, and consumer acknowledgement policy are
  not consolidated in one executable contract policy.

### 2.4 Event contracts

- Ledger publishes `ledger.transaction.posted.v1`,
  `ledger.transaction.reversed.v1`, and `ledger.adjustment.decided.v1` through
  the transactional outbox.
- The event package contains versioned routing-key constants and Go payload
  structs. Golden tests protect selected JSON bytes.
- Fraud and notification consume `ledger.transaction.posted.v1` and deduplicate
  using the AMQP `message_id`, which is the outbox row UUID.
- Go's standard `json.Unmarshal` ignores unknown fields, but tolerant-reader
  behavior and schema-version handling are not explicitly tested.
- `docs/reference/events.md` is useful but can drift from the live structs. There is no
  machine-readable event catalog, JSON Schema, or semantic breaking check.

### 2.5 Existing CI baseline

- CI and local verification already include Go lint/test/vet, integration tests,
  full-stack journeys, chaos scenarios, and protobuf checks.
- Contract tooling must extend those gates without replacing business tests:
  schema compatibility cannot prove authorization, idempotency, or monetary
  correctness.

## 3. Scope and anti-scope

### In scope

- a complete owner/audience route and consumer inventory;
- canonical OpenAPI 3.1 contracts for public, admin, webhook, and direct internal
  HTTP surfaces;
- route registration metadata and route-to-operation coverage checks;
- explicit HTTP transport DTOs, shared error codes, and normalized API errors;
- request and response conformance tests using real routers;
- semantic HTTP compatibility checks against the merge base;
- major-version, deprecation, sunset, migration, and retirement policy;
- the existing Buf protobuf gate plus explicit v1/v2 rollout rules;
- a versioned event catalog, JSON Schemas, compatibility checks, and
  tolerant-reader tests;
- event dual-publish safety through a stable logical event identifier;
- pinned local/CI tooling, runbooks, metrics, and evidence;
- a hard readiness gate for C1.

### Out of scope

- implementing merchant/B2B endpoints, API keys, quotas, or outbound webhooks
  from C1;
- generating or publishing client SDKs;
- GraphQL, AsyncAPI, Avro, Kafka, or a hosted schema registry;
- changing service ownership or replacing `net/http` with a framework;
- generating application handlers or domain models from OpenAPI;
- versioning database schemas for external consumers;
- redesigning existing business semantics merely to make a specification
  prettier;
- treating contract tests as substitutes for authorization, integration,
  smoke, business, or chaos tests;
- retiring any live v1 contract as part of the A9 baseline;
- creating a fake production v2 event solely to demonstrate the mechanism.

## 4. Locked contract model

### 4.1 Contract families

| Family | Canonical artifact | Compatibility boundary |
| --- | --- | --- |
| Public user HTTP | `api/openapi/public-v1.yaml` | Auth and gateway user APIs, including proxied ledger operations |
| Vendor callback HTTP | `api/openapi/webhooks-v1.yaml` | Inbound payment-vendor callbacks and signature/error behavior |
| Admin BFF HTTP | `api/openapi/admin-v1.yaml` | JSON/form endpoints used by the operator console |
| Direct internal HTTP | `api/openapi/internal-v1.yaml` | Service admin APIs called by Admin BFF or operational tooling |
| gRPC | `api/proto/seev/<owner>/vN/*.proto` | Internal service RPC packages protected by Buf |
| AMQP events | `api/events/catalog.yaml` plus `api/events/**/vN.schema.json` | Routing key, payload, delivery, and consumer contract |

Health, readiness, metrics, and browser HTML routes remain in the route
inventory but are classified as `operational` or `browser`, not represented as
business OpenAPI operations. CSV, multipart, and binary API responses are
business operations and must be represented accurately.

### 4.2 Compatibility vocabulary

- **Additive:** an existing conforming consumer continues to behave correctly
  without being redeployed.
- **Breaking:** a conforming request may be rejected, a conforming response can
  no longer be decoded or interpreted, or an observable meaning changes.
- **Behavioral breaking:** wire syntax may be unchanged, but authorization,
  idempotency, ordering, retryability, side effects, units, defaults, or error
  semantics change.
- **Deprecated:** still operational and behaviorally compatible, but a supported
  replacement and migration path exist.
- **Sunset:** a future retirement date has been announced; this is distinct from
  deprecation.
- **Retired:** no longer served or published after all retirement gates pass.

Compatibility applies to semantics as well as field shapes. A change from minor
units to major units, from at-least-once to at-most-once, or from retryable 503
to permanent 422 is breaking even if the JSON type is unchanged.

## 5. Locked design decisions

### K1 — Every boundary has an owner, audience, and consumer

Add `api/contracts/surfaces.yaml` with one entry per HTTP operation, RPC, and
event. Each entry records:

- stable contract ID and owner service;
- audience (`public`, `vendor`, `admin`, `internal`, `browser`, or
  `operational`);
- canonical artifact and operation/rpc/routing-key name;
- authentication and authorization mechanism;
- known consumer and owner contact identifier;
- lifecycle state (`active`, `deprecated`, `sunset`, or `retired`);
- introduced contract version and, when applicable, replacement;
- data classification and whether examples may contain personal data.

Unknown ownership or audience fails CI. A new route, RPC, or routing key cannot
merge until its inventory entry and canonical contract are present. Consumer
entries are repository identifiers such as `gateway`, `admin-bff`, `fraud`,
`notify`, `operator-cli`, or `external`; they are never email addresses or
personal names.

### K2 — Four OpenAPI 3.1 source documents are canonical

Use OpenAPI 3.1 YAML for the four HTTP families in section 4.1. Shared schemas,
parameters, headers, and security schemes live under
`api/openapi/components/`. Relative references are allowed in source files;
deterministically bundled files are written to `api/openapi/dist/` and committed
for simple public consumption.

The public document contains separate auth and gateway server entries. Each
operation declares its owning server at operation level so the combined public
contract never implies that every route is served by both processes.

Every operation defines:

- globally unique, stable `operationId` prefixed by owner;
- owner and audience extensions;
- authentication/security requirements, including unauthenticated cases;
- path/query/header/cookie parameters and unknown-field behavior;
- content type, body-size expectation, request and response schemas;
- all expected success and business/error status codes;
- standard request-ID, rate-limit, deprecation, and sunset headers where
  applicable;
- explicit monetary units and string/integer representation;
- redacted examples containing no live credentials or personal data.

Source files are reviewed; bundled files are generated. CI regenerates bundles
and fails on a diff. No runtime Swagger UI is added to production listeners.
Static local rendering may be provided through a Make target.

### K3 — Route registration metadata proves coverage

Add a small `pkg/httpcontract` registration wrapper around `http.ServeMux`. It
records method, route pattern, owner, audience, and `operationId` while
delegating to the standard mux. It must preserve Go 1.22 matching, path values,
middleware order, and existing proxy behavior.

Migrate all route registration sites to the wrapper, including conditional
routes. A full-dependency test build records the complete route set and compares
it with `surfaces.yaml` and OpenAPI:

- each business API route maps to one and only one OpenAPI operation;
- each OpenAPI operation maps to a live route;
- operational/browser exclusions are explicit, not inferred;
- duplicate method/path or operation IDs fail;
- a conditionally unavailable module cannot silently remove its contract from
  the full router fixture.

The wrapper is inventory machinery only. It does not perform authorization,
generate handlers, or introduce dynamic route configuration.

### K4 — HTTP wire types are explicit and errors are uniform

API handlers use transport DTOs rather than serializing repository/domain
models directly. Dynamic `map[string]any` fields are allowed only for explicitly
documented metadata objects with bounded schemas.

JSON success responses retain the existing envelope:

```json
{"success":true,"data":{},"meta":{}}
```

JSON errors use:

```json
{"success":false,"error":{"code":"STABLE_CODE","message":"human-readable message","details":{}}}
```

`code`, HTTP status, and documented detail keys are contractual. Human-readable
message text is not a parsing surface. Add `api/contracts/errors.yaml` as the
machine-readable registry of stable codes, allowed statuses, owners, and
retryability.

Normalize API middleware, 404, 405, rate-limit, KYC, assurance, proxy, and
dependency failures through `pkg/response`. HTML pages may continue to return
HTML/plain-text errors, and 204/CSV/binary responses remain unenveloped when the
OpenAPI operation says so.

Servers continue to reject unknown request fields. Clients and event consumers
must ignore unknown response/event fields. These asymmetric rules are tested.

### K5 — HTTP compatibility is semantic and fail-closed

Pin an OpenAPI linter and semantic-diff tool in repository tooling. The PR gate
compares source contracts with the merge-base version, not a developer's
possibly stale local `main`.

Within an existing major version, allowed changes are limited to:

- adding an operation at a new non-conflicting path;
- adding an optional request property with no behavior-changing default;
- adding an optional response property that tolerant clients can ignore;
- documenting an already returned status/header without changing behavior,
  after conformance evidence proves it is truly existing behavior;
- widening non-security documentation or examples.

The following require a new major version or a parallel replacement operation:

- removing/renaming a path, method, parameter, field, status, or media type;
- making an optional input required or tightening validation/ranges;
- changing a type, format, nullability, default, money unit, timestamp meaning,
  pagination, sorting, authorization, idempotency, or retry semantics;
- removing a response field or changing an error code/status mapping;
- making security less strict in a way that changes the trust boundary;
- changing webhook signature construction or replay rules.

There is no `breaking-change-approved` bypass. An intentional break introduces
and retains a versioned replacement so the compatibility diff remains safe.

### K6 — HTTP major versions coexist; deprecation is standards-based

Public and Admin BFF major versions remain in URL paths (`/api/v1`, `/api/v2`).
Do not negotiate major versions through custom headers or content types. The
existing unversioned `/webhooks/{vendor}` and direct `/admin/...` paths are
grandfathered as their v1 contract to avoid a cosmetic rename. A breaking
replacement uses a parallel explicit path such as `/webhooks/v2/{vendor}` or
`/admin/v2/...`; it never changes the old path in place. Routers and proxies
serve old and replacement operations together until retirement completes.

Add `api/contracts/deprecations.yaml` and operation-aware middleware. Deprecated
responses use:

```text
Deprecation: @<unix-seconds>
Sunset: <HTTP-date>
Link: <https://.../migration-guide>; rel="deprecation"; type="text/html"
```

`Deprecation` follows
[RFC 9745](https://www.rfc-editor.org/rfc/rfc9745.html); `Sunset` follows
[RFC 8594](https://www.rfc-editor.org/rfc/rfc8594.html). Sunset must not precede
deprecation, and deprecation alone does not change endpoint behavior.

Minimum replacement windows are:

| Audience | Minimum window before retirement |
| --- | --- |
| Current public user API | 90 days |
| Future merchant/B2B or vendor API | 180 days |
| Admin BFF and direct internal HTTP | 30 days after every registered consumer acknowledges migration |

Retirement additionally requires a documented replacement, migration guide,
all known consumer acknowledgements, and 30 consecutive days of zero measured
traffic to the deprecated operation. Test mode may shorten clocks; production
mode may not. Removing a deprecated path before these gates fails CI.

### K7 — Protobuf remains canonical and uses package-major evolution

Keep `.proto` files as the sole gRPC source of truth and committed generated Go
as derived output. Buf STANDARD lint and FILE breaking checks remain mandatory.

Within `seev.<owner>.v1`:

- only additive fields/RPCs are allowed;
- field numbers and names are never reused and removed fields are reserved;
- enum zero values represent unspecified/unknown behavior;
- adding enum values requires consumers to handle unknown numeric values;
- changing units, defaults, idempotency, authorization, or error mapping is
  behaviorally breaking even when Buf permits it;
- generated output must be clean after `buf generate`.

A breaking change creates `seev.<owner>.v2`. Server registration and clients
support v1 and v2 concurrently, with independent metrics. Internal gRPC v1 may
be removed only after every registered consumer acknowledges v2 and v1 has zero
calls for 30 consecutive days. Mark deprecated protobuf elements with the
standard deprecated option; do not edit generated files manually.

Add `api/contracts/proto-semantics.yaml` for behavior Buf cannot infer, including
money units, authentication, authorization, idempotency, retryability, and error
mapping per RPC. Every RPC must have an entry, and semantic changes are reviewed
by the same merge-base compatibility gate.

### K8 — JSON Schema and a catalog are canonical for events

Add `api/events/catalog.yaml`. Each routing key records producer, payload schema,
schema version, aggregate, delivery guarantee, ordering guarantee, retry/DLQ
behavior, deduplication key, data classification, consumers, and lifecycle.

Use JSON Schema 2020-12 under paths such as:

```text
api/events/ledger/transaction-posted/v1.schema.json
api/events/ledger/transaction-reversed/v1.schema.json
api/events/ledger/adjustment-decided/v1.schema.json
```

The schemas are canonical; Go event structs remain hand-written implementation
types. Producer golden payloads must validate against schemas, and each consumer
fixture must validate before its handler test runs. `docs/reference/events.md` is generated
or checked from the catalog and schema metadata so it cannot silently drift.

All event schemas explicitly define required versus optional properties,
formats, money units, timestamp format, vocabulary openness, and unknown-field
policy. Examples contain synthetic UUIDs and no personal data.

### K9 — Event compatibility uses additive v1 and a safe dual-version path

The same routing key/version permits only optional additive properties. The
following require a new `.v2` routing key and schema:

- removing, renaming, or requiring a property;
- changing a type, format, unit, nullability, or meaning;
- changing aggregate/routing semantics, delivery guarantee, or ordering;
- narrowing accepted values;
- adding an enum value unless that vocabulary is explicitly marked open and
  every consumer has an unknown-value behavior test.

A9 adds an optional `event_id` logical UUID to every existing v1 event. Producers
generate it once per logical domain event. Consumers deduplicate by `event_id`
when present and fall back to AMQP `message_id` for historical payloads.

For a future v1→v2 migration:

1. add `event_id` support and tolerant-reader behavior to all v1 consumers;
2. publish the v2 schema and consumer code without changing v1 behavior;
3. atomically write distinct v1/v2 outbox rows carrying the same `event_id`;
4. move consumers to version-specific queues and v2, retaining logical-event
   deduplication during replica overlap;
5. measure delivery and effect parity by logical event ID;
6. stop v1 publication only after all consumers acknowledge and v1 usage is
   zero for 30 consecutive days;
7. retain the retired schema and migration record in Git.

Distinct outbox rows keep distinct AMQP `message_id` values; `event_id` prevents
the two representations of one logical event from producing duplicate business
effects. A9 tests this mechanism with contract fixtures and an integration
harness, not a fake production v2 routing key.

### K10 — Readers are tolerant without accepting invalid meaning

HTTP clients and event consumers must tolerate:

- unknown optional response/event fields;
- JSON property reordering;
- omitted optional fields;
- a documented open-vocabulary value by mapping it to an explicit unknown or
  ignored path.

They must not apply side effects for malformed required fields, invalid money,
an unsupported routing-key version, or a payload whose embedded schema version
contradicts its routing key. Known-version poison payloads follow the existing
bounded retry/dead-letter or operator-recovery policy; they are never silently
acknowledged after a partial effect.

Request servers remain strict: unknown client fields return the documented 400
error. Tolerance is not permission to reinterpret invalid data.

### K11 — Contract fixtures are safe, deterministic, and reviewable

Store synthetic fixtures under `api/contracts/testdata/` by family and
operation. Every fixture records the contract version and expected status or
consumer result. UUIDs, timestamps, and monetary values are deterministic.

Required negative fixtures cover authentication, authorization, unknown input
fields, invalid enum/format, idempotency conflict, rate limiting, and unavailable
dependencies where those outcomes belong to the operation. Secrets, real JWTs,
real vendor signatures, and personal data are generated at test time or use
obviously synthetic values.

Snapshot/golden updates require an accompanying canonical contract diff. A test
cannot auto-accept changed output.

### K12 — One local and CI gate covers all contract families

Add pinned targets:

```text
make contract-tools       # install exact reviewed tool versions
make contract-generate    # deterministic OpenAPI bundles/docs artifacts
make contract-lint        # OpenAPI, JSON Schema, catalog, inventory
make contract-breaking    # HTTP + event + Buf comparison to merge base
make contract-test        # route coverage and producer/consumer conformance
make contracts            # all of the above except tool installation
```

The PR workflow fetches enough history to resolve the merge base and runs
`make contracts`. Tool versions are pinned in source/Makefile and CI caching
keys; no `latest` downloads are allowed. The initial A9 bootstrap records the
first OpenAPI/event baseline. After that commit, a missing base artifact is an
error rather than permission to skip comparison.

The gate includes synthetic mutation tests proving that allowed additions pass
and forbidden changes fail. It reports artifact and operation names but no
request bodies, tokens, or personal data.

### K13 — Contract observability is bounded and retirement-driven

Use low-cardinality metrics:

```text
seev_http_contract_requests_total{audience,api_version,operation_id,status_class}
seev_grpc_contract_requests_total{owner,api_version,method,result}
seev_event_contract_messages_total{event_type,schema_version,result}
seev_event_contract_effects_total{consumer,event_type,schema_version,result}
seev_contract_validation_failures_total{family,contract_id,reason}
seev_deprecated_contract_requests_total{family,contract_id,version}
```

Operation IDs and contract IDs come from bounded checked-in registries. Never use
raw paths, user IDs, account IDs, request IDs, event IDs, routing parameters, or
error messages as labels. Contract validation failures log only contract ID,
request correlation ID, controlled reason, and redacted location.

Dashboards show active/deprecated version traffic, validation failures, dual-
publish parity, and registered-consumer migration state. Metrics are evidence
for retirement, not an automatic deletion switch.

### K14 — C1 cannot begin until the A9 baseline is green

C1 may add new public/B2B contracts only after:

- all current HTTP operations are inventoried and covered;
- the compatibility gate is mandatory in PR CI;
- event and protobuf evolution drills pass;
- the deprecation/retirement policy is documented and tested;
- there is no unresolved contract drift in the current v1 baseline.

A9 does not need to retire anything. Its job is to make future additions and
migrations safe before external consumers exist.

## 6. Execution tasks

Execute T0 → T1 → T2 → T3 → T4 → T5 → T6. T1 and T4 may be developed in
parallel after T0, but the compatibility baseline is committed only after all
families agree on ownership and terminology.

### T0 — Inventory routes, consumers, and current wire behavior

**Work**

1. Enumerate every route from all eight service routers, including conditional,
   proxied, admin, webhook, browser, health, readiness, and metrics routes.
2. Enumerate every RPC, protobuf caller, event routing key, producer, queue, and
   consumer.
3. Capture current method/path, auth, content type, request/response DTO, status,
   error code, pagination, and idempotency behavior.
4. Create `api/contracts/surfaces.yaml` and the initial consumer ownership map.
5. Identify domain structs and ad hoc maps currently crossing API boundaries.
6. Record known inconsistencies separately; do not encode accidental/plain-text
   errors as the desired canonical baseline.

**Required tests/checks**

- all full-dependency routers are instantiated without external network calls;
- source registration count and inventory count match by audience;
- every proto service/method and event constant is inventoried;
- proxy ownership is unambiguous: gateway/Admin BFF is the edge, downstream
  service remains behavior owner;
- no fixture or inventory value contains a credential or real personal data;
- `git diff --check` passes.

**Definition of done:** the repository has one reviewed map of every consumed
boundary and every known consumer, with no unclassified route/RPC/event.

### Result

_Pending implementation._

### T1 — Canonical OpenAPI and normalized HTTP wire types (K2, K4)

**Work**

1. Add shared OpenAPI components and the four source specifications.
2. Model every JSON, multipart, CSV, and binary API operation, including auth,
   security, errors, headers, units, pagination, and examples.
3. Add explicit transport DTOs where handlers currently expose domain structs
   or unbounded maps.
4. Create `api/contracts/errors.yaml` and normalize API middleware/handlers,
   including JSON 404/405 behavior, through `pkg/response`.
5. Preserve HTML, probe, metrics, CSV, and binary behavior where intentionally
   excluded from the JSON envelope.
6. Add deterministic bundling and local static documentation generation.

**Required tests**

- every source and bundled OpenAPI document validates as 3.1;
- bundled output is deterministic across two clean generations;
- request schemas reject unknown fields consistently with live handlers;
- every documented response status/content type matches a live handler fixture;
- all API errors use registered code/status pairs and the standard envelope;
- CSV, multipart, binary, 204, and HTML exceptions retain their documented
  content types and bodies;
- examples pass schema validation and secret/PII scans.

**Definition of done:** current intended HTTP behavior has a readable canonical
contract, and accidental error/DTO inconsistency is removed before freezing v1.

### Result

_Pending implementation._

### T2 — Route coverage and live HTTP conformance (K1, K3, K10–K11)

**Work**

1. Add the `pkg/httpcontract` mux wrapper and migrate every registration site
   without changing route or middleware behavior.
2. Build full-dependency route snapshots for auth, gateway, ledger, pay-in,
   payout, fraud, assurance, and Admin BFF.
3. Add bidirectional route/inventory/OpenAPI coverage tests.
4. Add request/response validation helpers around `httptest` exchanges and
   selected full-stack calls.
5. Add at least one positive fixture per operation and required negative
   fixtures for its declared errors.
6. Add tolerant internal-client tests for unknown response fields while keeping
   server request decoding strict.

**Required tests**

- exact Go 1.22 route matching, path values, redirects, 404, and 405 remain
  unchanged except for the deliberately normalized error representation;
- adding an unclassified route or an orphan OpenAPI operation fails;
- duplicate operation IDs and duplicate method/path entries fail;
- conditional modules are all present in the full router fixture;
- gateway and Admin BFF proxy responses conform at their exposed edge;
- IDOR, missing auth, wrong role, missing CSRF, invalid signature, and unknown
  request-field fixtures return the documented contract;
- no validation helper logs sensitive request/response bodies.

**Definition of done:** a handler cannot be added or changed without an owned
contract and a live conformance test.

### Result

_Pending implementation._

### T3 — HTTP compatibility, deprecation, and retirement drill (K5–K6, K12–K13)

**Work**

1. Pin OpenAPI lint/diff tools and add generate, lint, breaking, test, and
   aggregate Make targets.
2. Compare contracts to the Git merge base in local scripts and PR CI.
3. Add synthetic additive and breaking mutation fixtures for paths, fields,
   status codes, security, units, and validation.
4. Add `api/contracts/deprecations.yaml`, standards-compliant middleware, and
   configuration validation.
5. Add version/operation metrics and a dashboard panel for deprecated usage.
6. Run a test-only v1→v2 operation drill: coexistence, headers, replacement
   link, consumer acknowledgement, zero-use window, and blocked early removal.
7. Write the HTTP evolution and retirement runbook.

**Required tests**

- optional additive changes pass while every K5 breaking class fails;
- comparison uses merge base on both branch and pull-request workflows;
- `Deprecation` structured date, `Sunset` HTTP-date, ordering, and migration
  `Link` validate;
- deprecated operations preserve behavior while adding metadata;
- production configuration rejects shortened minimum windows;
- retirement fails with traffic, a missing consumer acknowledgement, missing
  replacement, missing guide, or an incomplete time window;
- metrics contain only registered low-cardinality values.

**Definition of done:** HTTP compatibility and retirement are enforced by code
and CI rather than reviewer memory.

### Result

_Pending implementation._

### T4 — Event schemas, tolerant consumers, and evolution drill (K8–K11)

**Work**

1. Add the event catalog and JSON Schemas for all three current ledger events.
2. Reconcile `docs/reference/events.md`, Go structs, golden payloads, routing keys, and
   consumer behavior with the canonical schemas.
3. Add optional logical `event_id` to existing v1 payloads and generate one per
   logical event.
4. Update fraud/notification deduplication to prefer logical event ID and fall
   back to AMQP message ID for historical events.
5. Add producer schema validation, consumer fixtures, tolerant-reader tests,
   and embedded-version/routing-key consistency checks.
6. Add semantic event diff rules and synthetic additive/breaking mutations.
7. Build a test-only v1/v2 dual-outbox harness proving shared logical ID,
   distinct delivery IDs, consumer cutover, parity, and exactly-once business
   effect under duplicate delivery/restart.
8. Add event-version metrics and an evolution/retirement runbook.

**Required tests**

- all current producer payloads and historical golden fixtures validate;
- unknown optional and reordered fields are accepted;
- missing/invalid required fields and version mismatch cause no partial effect;
- old payloads without `event_id` still deduplicate by `message_id`;
- v1/v2 representations sharing `event_id` produce one business effect;
- transaction-posted fraud and notification behavior remains correct;
- optional additions pass; removals, required additions, type/unit/meaning,
  closed-enum, routing, and delivery changes fail;
- catalog, docs, schemas, constants, producers, queues, and consumers have no
  orphan entry.

**Definition of done:** event contracts are machine-checkable and a future
major-version migration cannot double-apply one logical event.

### Result

_Pending implementation._

### T5 — Protobuf policy and v1/v2 compatibility drill (K7, K10, K12)

**Work**

1. Reconcile every protobuf service/method with the surface/consumer inventory.
2. Add policy checks for package major, reserved removed fields, enum-zero
   behavior, generated-code cleanliness, and complete semantic metadata.
3. Make Buf comparison use the same explicit merge-base policy as HTTP/event
   contracts.
4. Add synthetic allowed and forbidden proto mutation fixtures.
5. Add a test-only v1/v2 server/client rollout drill with concurrent
   registration, independent metrics, fallback/rollback, and consumer
   acknowledgement.
6. Document additive, deprecation, and retirement procedures for internal RPCs.

**Required tests**

- current proto generation/lint/breaking checks pass from a clean tree;
- adding a field/RPC passes, while renumber/remove/type/package mutations fail;
- removed sample fields require reserved number and name;
- unknown enum values do not crash or silently authorize/move money;
- v1 and v2 can run concurrently and a v1-only client remains functional;
- early v1 removal fails the policy gate;
- generated files contain no manual drift.

**Definition of done:** Buf's wire checks and repository semantic/rollout policy
together protect every internal RPC consumer.

### Result

_Pending implementation._

### T6 — Operations, documentation, C1 readiness, and final gate (K12–K14)

**Work**

1. Add contract ownership/evolution documentation to the root README, the
   [project guide](../../development/project-guide.md), and contributor
   workflow.
2. Add local commands for lint, generation, compatibility, fixtures, and
   static API documentation.
3. Add dashboards/alerts for validation failures, deprecated traffic, and
   event dual-version parity without high-cardinality labels.
4. Run controlled failures: undocumented route, stale bundle, breaking HTTP
   response, malformed event, unsupported proto change, missing merge base,
   and premature retirement.
5. Run the complete product and contract gates from clean generated artifacts.
6. Record artifact counts, operation/RPC/event coverage, contract timings,
   mutation-test evidence, and final C1 readiness decision.
7. Mark A9 complete only after every acceptance item has evidence.

**Required final gate**

```bash
GOCACHE=/tmp/seev-go-cache go build ./...
GOCACHE=/tmp/seev-go-cache go vet ./...
GOCACHE=/tmp/seev-go-cache go vet -tags=integration ./...
GOCACHE=/tmp/seev-go-cache make test
GOCACHE=/tmp/seev-go-cache make lint
make contract-generate
make contract-lint
make contract-breaking
GOCACHE=/tmp/seev-go-cache make contract-test
make contracts
make proto
make proto-lint
make proto-breaking
GOCACHE=/tmp/seev-go-cache go test -tags=integration -race ./...
GOCACHE=/tmp/seev-go-cache ./scripts/smoke-test.sh all
GOCACHE=/tmp/seev-go-cache ./scripts/business-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/admin-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/chaos-test.sh all
git diff --check
```

**Definition of done:** every current consumed boundary is explicit, compatible,
observable, and protected in PR CI, so C1 can add external contracts without
depending on synchronized consumer deployments.

### Result

_Pending implementation._

## 7. Acceptance checklist

### Inventory and ownership

- [ ] Every HTTP route, RPC, and event has one owner, audience, lifecycle, and
      canonical artifact.
- [ ] Every non-operational contract has known consumers or an explicit
      `external` consumer class.
- [ ] Conditional and proxied routes have unambiguous edge and behavior owners.
- [ ] CI rejects unclassified and orphan contracts.

### HTTP contracts

- [ ] Four OpenAPI 3.1 source documents and deterministic bundles validate.
- [ ] Every business route has exactly one operation and a live fixture.
- [ ] Transport DTOs prevent accidental domain-field exposure.
- [ ] JSON error codes/statuses are registered and uniformly enveloped.
- [ ] Multipart, CSV, binary, 204, and HTML exceptions are explicit.
- [ ] Unknown request fields are rejected and unknown response fields are
      tolerated by clients.
- [ ] Additive changes pass and all locked breaking classes fail.

### Deprecation and retirement

- [ ] Deprecation, Sunset, and Link metadata conform to their documented syntax.
- [ ] Major versions coexist without changing old-version behavior.
- [ ] Minimum windows cannot be shortened in production mode.
- [ ] Retirement requires replacement, guide, acknowledgements, and zero use.
- [ ] No A9 task retires a current v1 operation.

### Events

- [ ] Every routing key has a valid catalog entry and JSON Schema.
- [ ] Producer, golden, and consumer fixtures all validate.
- [ ] Existing consumers tolerate optional additions and reject malformed known
      versions before side effects.
- [ ] Logical event IDs preserve one business effect across dual versions.
- [ ] Event semantic breaking mutations fail.
- [ ] Event docs cannot drift from schemas/catalog.

### gRPC

- [ ] Buf generation, lint, and breaking checks use an explicit merge-base
      policy and pass.
- [ ] Additive and forbidden synthetic mutations prove the gate.
- [ ] Reserved fields, enum-zero behavior, and package-major rules are enforced.
- [ ] The v1/v2 drill proves coexistence, rollback, metrics, and guarded removal.

### CI, security, and operations

- [ ] Contract tools are pinned and cached; no `latest` dependency is used.
- [ ] PR CI fails for stale generated bundles or unavailable base artifacts.
- [ ] Fixtures, examples, logs, and reports contain no secrets or personal data.
- [ ] Metrics use only registered low-cardinality labels.
- [ ] Contract failures, deprecation, event cutover, and retirement have runbooks.
- [ ] Full contract, proto, build, vet, lint, race, integration, smoke, business,
      admin, chaos, and diff gates are green.

## 8. Global Definition of Done

- [ ] T0–T6 results contain commands, concise evidence, timings, and commit IDs.
- [ ] The checked-in contracts match the live routers, RPCs, and event behavior.
- [ ] No compatibility check can be bypassed for an in-place breaking change.
- [ ] Existing money, auth, KYC, webhook, admin, and notification journeys retain
      their intended behavior.
- [ ] A9 is marked complete in the roadmap/index only after mandatory PR CI is
      green and the C1 readiness evidence is recorded.

## 9. Explicit follow-ups

The following remain outside A9:

1. merchant/B2B API contracts, API keys, quotas, and outbound webhook contracts
   from C1;
2. generated SDK publication and a public developer portal;
3. a hosted schema registry if independent producer repositories later make Git
   coordination insufficient;
4. AsyncAPI or Kafka/Avro contracts if the messaging platform changes;
5. CDC/warehouse schema evolution from C2;
6. database expand/contract migration machinery and shadow traffic from C6;
7. jurisdiction-specific API retention or legal notice requirements.
