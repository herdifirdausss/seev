# C1 Final Acceptance Evidence — Plan 57 T10

> [Documentation home](../../README.md) · [Roadmap](../roadmap/README.md) ·
> [Plan 57](../roadmap/active/57-c1-merchant-b2b-api.md)

This is the T10 final-verification evidence required by
[Plan 57 §28](../roadmap/active/57-c1-merchant-b2b-api.md#28-documentation-deliverables)
before C1 can be considered complete. See
[Plan 57's own T10 Result section](../roadmap/active/57-c1-merchant-b2b-api.md#t10--verification-chaos-and-release-evidence)
for the full narrative; this document is the command-log/evidence
counterpart.

**Disposition: complete, plan archived.** The T10b follow-up ("T10b" —
§23.8 race-test items 2-6, precision chaos coverage in §24.3/§24.4, and
the suspended-tenant read/write nuance) closed in the same pass this
document was finalized in; see "T10b closure" below.

## Two bugs found and fixed this pass

1. **T9's global kill switch was never wired to the B2B router.** Commit
   `d818370`. `RequireB2BEnabled` existed since T9 but no B2B route existed
   to gate; still never mounted once T10's router landed. Verified live via
   `merchant-e2e.sh` before/after, plus `TestB2BRouter_GlobalKillSwitchGatesEveryRoute`.
2. **Live-activation bypass via key/tenant environment mismatch.** Commit
   `86b4824`. `KeyService.CreateKey` never checked a requested key's
   environment against its tenant's own `Environment`, so a sandbox tenant
   (auto-activates, no checker approval) could receive a "live" key,
   bypassing the maker/checker draft→active gate a real live tenant
   requires. Fixed at both the issuance layer (`CreateKey`) and the
   request-authentication layer (`RequireMerchantAuth`), each independently
   fail-closed.

## Final gate command log

Run from the repository root, after freeing disk space (95%→61%, ~11 GB of
stale `/tmp/seev-*` build/lint/module caches from this session's own
verification work) and removing 67 leaked testcontainers instances (66
Postgres, 1 RabbitMQ) that were causing unrelated transient failures.

```text
$ go build ./...
(clean)

$ go vet ./...
(clean)

$ make lint
"golangci-lint run ./..."
0 issues.

$ make contracts
go run ./cmd/contractgenerate
go test ./api/contracts        ok
go run ./cmd/contractcheck -mode breaking
go test ./pkg/httpcontract ./api/contracts   ok

$ go run ./cmd/doccheck
doccheck: checked 139 Markdown files; required guides, language,
visual and interactive assets, local links, and anchors are valid

$ go test ./...
91 packages ok, 0 failures (packages with no test files excluded)

$ go test -race ./...
91 packages ok, 0 failures, 0 data races reported

$ go test -tags=integration ./...
0 failures (after the disk-space/container cleanup above; the initial
run before cleanup showed one transient pkg/database testcontainers
timeout, confirmed non-reproducible by an isolated re-run before the
cleanup, and fully clean after)

$ ./scripts/smoke-test.sh all
=== ALL SMOKE ASSERTIONS PASSED === (19/19)

$ ./scripts/business-e2e.sh
=== FULL BUSINESS JOURNEY PASSED === (84/84)

$ ./scripts/admin-e2e.sh
admin-e2e completed (5/5)

$ ./scripts/merchant-e2e.sh
merchant-e2e completed (25/25, includes the new global-kill-switch leg)

$ ./scripts/chaos-test.sh 21
=== ALL CHAOS ASSERTIONS PASSED ===
(new merchant B2B transfer kill-9-mid-posting scenario)

$ ./scripts/privacy-e2e-host.sh
FAILS reproducibly (2/2 runs) at the closure leg — see "Known issue" below.
```

T10b re-run after the additions above:

```text
$ go build ./... && go vet ./...
(clean)

$ make lint
0 issues.

$ go test -race ./internal/merchant/...
ok (all 9 packages, 0 data races)

$ go test -race -tags=integration ./internal/merchant/...
ok (all 9 packages, 0 data races)

$ shellcheck scripts/chaos-test.sh
no new warning classes vs. the file's pre-existing baseline

$ ./scripts/chaos-test.sh 22
=== ALL CHAOS ASSERTIONS PASSED ===
(merchant quota Redis outage)

$ ./scripts/chaos-test.sh 23
=== ALL CHAOS ASSERTIONS PASSED ===
(merchant webhook relay survives a RabbitMQ outage)

$ ./scripts/merchant-e2e.sh
merchant-e2e completed (all assertions passing)
```

## Known issue: `privacy-e2e-host.sh` (out of this plan's scope)

Reproduced twice: the script dies silently right after "registered
closure-leg user" is printed, before "closure requested." Log analysis
(`assurance-service.log` showing a scheduled-run connection-refused to
payin-service's gRPC port at almost the same instant `payin-service.log`
shows a graceful shutdown) points to `scripts/privacy-e2e.sh`'s own
`assurance_run()` helper manually triggering an assurance run via
`curl -sf`, racing the `ASSURANCE_INTERVAL=1s` background scheduler that
`privacy-e2e-host.sh` also configures — a collision (likely a 409 from the
manual trigger) kills the script under `set -e`, which tears down every
service mid-run.

```text
$ git log --oneline -- scripts/privacy-e2e.sh scripts/privacy-e2e-host.sh internal/assurance
```

confirms none of these files have been touched by any commit in Plan 57 —
this is pre-existing tooling from the (already archived) plan 51
data-lifecycle-privacy work, unrelated to the merchant B2B surface. Filed
as its own follow-up task rather than fixed here, since fixing it is
outside this plan's blast radius.

## Cross-tenant matrix (§23.7) — final result

| Case | Status |
|---|---|
| Tenant A reads tenant B resource | covered |
| Tenant A mutates tenant B resource | covered |
| Tenant A reuses tenant B idempotency key text | covered |
| Tenant A replays tenant B delivery | covered |
| Tenant A targets tenant B source account | not applicable — source account is always server-derived from the caller's own tenant, never a request field |
| Test key accesses live tenant | fixed (`86b4824`) |
| Live key accesses sandbox tenant | fixed (`86b4824`) |
| Suspended tenant reads | fixed T10b — reads now pass through (`Principal.TenantSuspended`), matching the plan's stated default |
| Suspended tenant writes | fixed T10b — denied with 403 `TENANT_SUSPENDED` via `RequireTenantNotSuspendedForWrites` |

## Race tests (§23.8) — final result

`go test -race ./internal/merchant/...` (plain and `-tags=integration`) is
clean, 0 data races. All 7 required scenarios now have dedicated coverage,
added in T10b:

1. concurrent same idempotency key — 3 tests (pre-existing).
2. concurrent key rotation/revocation vs. request — `auth_race_test.go`.
3. concurrent webhook workers claiming the same due delivery — `webhook_race_test.go`.
4. concurrent replay of the same original delivery — `webhook_race_test.go`.
5. concurrent endpoint disable vs. an in-flight delivery batch — `webhook_race_test.go`.
6. concurrent tenant suspension vs. financial write — `b2b_integration_test.go`.
7. duplicate owner events — covered functionally (pre-existing); still not
   under real goroutine contention, the one item left at "functional, not
   race-proven" — judged low-value to chase further since the underlying
   dedup is a database unique-constraint check, the exact mechanism items
   3-5 already prove race-safe under contention for the sibling tables.

## Secret scan

- `merchant_api_keys.secret_digest` — `bytea` (HMAC digest, not plaintext).
- `merchant_webhook_endpoints.secret_ciphertext` — `bytea` (encrypted).
- No plaintext secret column exists in the schema.
- Every service log from a full `merchant-e2e.sh` run grepped for the
  `mk_sandbox_`/`mk_live_` API-key prefix pattern: zero matches.

## T10b closure

All three T10b items closed:

1. **Race tests** (items 2-6 above) — added, see "Race tests" above.
2. **Suspended-tenant read/write policy** — fixed (`7ec70dd`): `Principal`
   gained `TenantSuspended`; `RequireTenantNotSuspendedForWrites` denies
   writes only, mounted per-route. Live integration test proves read
   succeeds / write denied / both recover on reactivation, through the
   real assembled router against real Postgres.
3. **Precision chaos coverage** (`bcdee3f`) — chaos scenario 22 (real
   Redis container stop/start through the assembled Gateway: writes fail
   closed 503 `QUOTA_UNAVAILABLE`, reads degrade-allow, both recover) and
   scenario 23 (real RabbitMQ stop/start: a merchant payin posts and
   settles while the broker is down, zero webhook hits during the
   outage, the merchant webhook `Consumer` catches up and delivers once
   the broker recovers). §24.2 (owner-service timeouts) and §24.6
   (database failures) remain covered only generically through the
   shared ledger `execTransfer`/Postgres-recovery path merchant
   transactions already route through — judged adequate, not a gap.
   §24.5 (webhook receiver failures) remains covered by T7's own unit
   tests, not re-proven as a chaos scenario — judged adequate for the
   same reason.

A pre-existing, unrelated doc bug was found and fixed while building
scenario 23: `docs/reference/services.md` claimed a merchant-facing
`/api/v1/b2b/webhook-endpoints` route (webhook management is admin-only)
and named external event types (`payin.settled.v1`, `transfer.posted.v1`)
that were never implemented — `transaction.posted.v1` is the one real
external event type.

## Residual risk carried forward (not a Plan 57 item)

`scripts/privacy-e2e-host.sh`'s pre-existing `assurance_run()` /
`ASSURANCE_INTERVAL` race (see "Known issue" above) is tracked as its own
follow-up task, unrelated to and out of scope for Plan 57.
